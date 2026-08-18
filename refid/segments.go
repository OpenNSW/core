// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// segment is the internal interface satisfied by each segment type.
// render is called once per Generate call in the order the segments appear in
// the format, and its output is concatenated to form the full ID.
//
//   - params holds the caller-supplied key/value pairs (e.g. officeCode: "COL").
//   - now is provided by the caller rather than read from time.Now() inside each
//     segment, so every segment in one Generate call shares the same clock value
//     and tests can inject a fixed time without global state.
type segment interface {
	validate(params map[string]string, now time.Time) error
	render(ctx context.Context, params map[string]string, now time.Time) (string, error)
}

// -----------------------------------------------------------------------
// literalSegment
// -----------------------------------------------------------------------

// literalSegment emits a fixed string, unchanged.
type literalSegment struct {
	value string
}

func (s *literalSegment) validate(_ map[string]string, _ time.Time) error {
	return nil
}

func (s *literalSegment) render(_ context.Context, _ map[string]string, _ time.Time) (string, error) {
	return s.value, nil
}

// newLiteralSegment constructs a literal segment and validates it has a value.
func newLiteralSegment(cfg SegmentConfig) (*literalSegment, error) {
	if cfg.Value == "" {
		return nil, fmt.Errorf("refid: literal segment requires a non-empty value")
	}
	return &literalSegment{value: cfg.Value}, nil
}

// -----------------------------------------------------------------------
// listSegment
// -----------------------------------------------------------------------

// listSegment validates a caller-supplied param value against a controlled list.
type listSegment struct {
	paramKey string
	allowed  map[string]struct{} // set for O(1) lookup
}

func (s *listSegment) validate(params map[string]string, _ time.Time) error {
	val, ok := params[s.paramKey]
	if !ok || val == "" {
		return fmt.Errorf("%w: param %q is required", ErrInvalidParam, s.paramKey)
	}
	if _, valid := s.allowed[val]; !valid {
		return fmt.Errorf("%w: value %q for param %q is not in the allowed list", ErrInvalidParam, val, s.paramKey)
	}
	return nil
}

func (s *listSegment) render(_ context.Context, params map[string]string, now time.Time) (string, error) {
	if err := s.validate(params, now); err != nil {
		return "", err
	}
	return params[s.paramKey], nil
}

// newListSegment constructs a list segment from config and the resolved allowed values.
func newListSegment(cfg SegmentConfig, values []string) (*listSegment, error) {
	if cfg.Param == "" {
		return nil, fmt.Errorf("refid: list segment requires a non-empty param")
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("refid: list segment references list %q which is empty or undefined", cfg.List)
	}
	allowed := make(map[string]struct{}, len(values))
	for _, v := range values {
		allowed[v] = struct{}{}
	}
	return &listSegment{paramKey: cfg.Param, allowed: allowed}, nil
}

// -----------------------------------------------------------------------
// dateSegment
// -----------------------------------------------------------------------

// dateSegment formats the current time using a Go reference-date layout string.
type dateSegment struct {
	layout string
}

func (s *dateSegment) validate(_ map[string]string, _ time.Time) error {
	return nil
}

func (s *dateSegment) render(_ context.Context, _ map[string]string, now time.Time) (string, error) {
	return now.Format(s.layout), nil
}

// newDateSegment constructs a date segment and validates that a layout is provided.
func newDateSegment(cfg SegmentConfig) (*dateSegment, error) {
	if cfg.Layout == "" {
		return nil, fmt.Errorf("refid: date segment requires a non-empty layout")
	}
	return &dateSegment{layout: cfg.Layout}, nil
}

// -----------------------------------------------------------------------
// sequenceSegment
// -----------------------------------------------------------------------

// sequenceSegment increments a durable counter and emits the zero-padded value.
//
// The scope key template may contain any of the following placeholders:
//
//	{issuer}    — the issuer string for this format
//	{idType}    — the idType string for this format
//	{yyyy}      — four-digit year derived from now
//	{yyyyMM}    — year + month derived from now
//	{yyyyMMdd}  — year + month + day derived from now
//	{<param>}   — any caller-supplied param key not already claimed above
//
// Reserved placeholders always take precedence over a caller param of the
// same name.
type sequenceSegment struct {
	issuer       string
	idType       string
	scopeKeyTmpl string
	padding      int
	store        SequenceStore
}

func (s *sequenceSegment) validate(params map[string]string, now time.Time) error {
	_, err := resolveScopeKey(s.scopeKeyTmpl, s.issuer, s.idType, params, now)
	return err
}

func (s *sequenceSegment) render(ctx context.Context, params map[string]string, now time.Time) (string, error) {
	key, err := resolveScopeKey(s.scopeKeyTmpl, s.issuer, s.idType, params, now)
	if err != nil {
		return "", err
	}

	counter, err := s.store.Next(ctx, key)
	if err != nil {
		return "", fmt.Errorf("refid: sequence store error for scope %q: %w", key, err)
	}

	// Enforce padding: if the counter value needs more digits than padding allows,
	// return ErrCounterOverflow rather than silently producing a longer-than-expected ID.
	padding := s.padding
	if padding < 1 {
		padding = 1
	}
	maxValue := int64(math.Pow10(padding)) - 1
	if counter > maxValue {
		return "", fmt.Errorf("%w: scope %q counter %d exceeds max %d for padding %d",
			ErrCounterOverflow, key, counter, maxValue, padding)
	}

	return fmt.Sprintf("%0*d", padding, counter), nil
}

// newSequenceSegment constructs a sequence segment, associating it with the
// store and binding the issuer/idType from the enclosing format.
func newSequenceSegment(cfg SegmentConfig, issuer, idType string, store SequenceStore) (*sequenceSegment, error) {
	if cfg.ScopeKey == "" {
		return nil, fmt.Errorf("refid: sequence segment requires a non-empty scopeKey")
	}
	if store == nil {
		return nil, fmt.Errorf("refid: sequence segment requires a non-nil SequenceStore")
	}
	padding := cfg.Padding
	if padding < 1 {
		padding = 1
	}
	return &sequenceSegment{
		issuer:       issuer,
		idType:       idType,
		scopeKeyTmpl: cfg.ScopeKey,
		padding:      padding,
		store:        store,
	}, nil
}

// -----------------------------------------------------------------------
// scopeKey template resolution
// -----------------------------------------------------------------------

// resolveScopeKey substitutes all {placeholder} tokens in tmpl and returns
// ErrInvalidParam if any un-substituted placeholder remains in the key.
// Precedence: reserved tokens ({issuer}, {idType}, date tokens) > caller params.
func resolveScopeKey(tmpl, issuer, idType string, params map[string]string, now time.Time) (string, error) {
	var replacements []string

	// Reserved placeholders (highest precedence; added first so strings.NewReplacer
	// picks these before any identically-named param).
	replacements = append(replacements,
		"{issuer}", issuer,
		"{idType}", idType,
		"{yyyy}", now.Format("2006"),
		"{yyyyMM}", now.Format("200601"),
		"{yyyyMMdd}", now.Format("20060102"),
	)

	// Caller-supplied params (lower precedence).
	for k, v := range params {
		replacements = append(replacements, "{"+k+"}", v)
	}

	key := strings.NewReplacer(replacements...).Replace(tmpl)

	if open := strings.IndexByte(key, '{'); open != -1 {
		if closeIdx := strings.IndexByte(key[open:], '}'); closeIdx > 1 {
			return "", fmt.Errorf("%w: scope key %q contains unresolved placeholder %q",
				ErrInvalidParam, key, key[open:open+closeIdx+1])
		}
	}

	return key, nil
}
