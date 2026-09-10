// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
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

	// isStateful reports whether render performs a durable, non-rollback-able
	// side effect (a sequence counter increment, a random value reservation).
	// compileFormat uses this to cap a format at one stateful segment: since
	// every other segment type is a pure function of (params, now) and cannot
	// fail during render once validate has already passed, capping the count
	// at one rules out a later segment failing after an earlier one has
	// already committed its side effect.
	isStateful() bool
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

func (s *literalSegment) isStateful() bool { return false }

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

func (s *listSegment) isStateful() bool { return false }

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

func (s *dateSegment) isStateful() bool { return false }

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

	maxValue := int64(math.Pow10(s.padding)) - 1

	counter, err := s.store.Next(ctx, key, maxValue)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%0*d", s.padding, counter), nil
}

func (s *sequenceSegment) isStateful() bool { return true }

// newSequenceSegment constructs a sequence segment, associating it with the
// store and binding the issuer/idType from the enclosing format.
func newSequenceSegment(cfg SegmentConfig, issuer, idType string, store SequenceStore) (*sequenceSegment, error) {
	if cfg.Sequence == nil {
		return nil, fmt.Errorf("refid: sequence segment requires a non-nil sequence config")
	}
	if cfg.Sequence.ScopeKey == "" {
		return nil, fmt.Errorf("refid: sequence segment requires a non-empty scopeKey")
	}
	if store == nil {
		return nil, fmt.Errorf("refid: sequence segment requires a non-nil SequenceStore")
	}
	if cfg.Sequence.Padding < 1 || cfg.Sequence.Padding > 18 {
		return nil, fmt.Errorf("refid: sequence segment padding must be between 1 and 18, got %d", cfg.Sequence.Padding)
	}
	return &sequenceSegment{
		issuer:       issuer,
		idType:       idType,
		scopeKeyTmpl: cfg.Sequence.ScopeKey,
		padding:      cfg.Sequence.Padding,
		store:        store,
	}, nil
}

// -----------------------------------------------------------------------
// randomSegment
// -----------------------------------------------------------------------

// Charset names accepted by a random segment's Charset config field.
const (
	CharsetNumeric      = "numeric"
	CharsetAlpha        = "alpha"
	CharsetAlphanumeric = "alphanumeric"
)

// randomAlphabets maps each supported charset name to its character set.
var randomAlphabets = map[string]string{
	CharsetNumeric:      "0123456789",
	CharsetAlpha:        "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	CharsetAlphanumeric: "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
}

// defaultRandomMaxAttempts is used when a random segment's config does not
// set MaxAttempts.
const defaultRandomMaxAttempts = 10

// randomSegment generates a fixed-length random string from a charset and
// reserves it in a RandomStore to guarantee uniqueness within its scope,
// retrying with a new value on collision.
//
// The scope key template accepts the same placeholders as sequenceSegment;
// see resolveScopeKey.
type randomSegment struct {
	issuer       string
	idType       string
	scopeKeyTmpl string
	length       int
	alphabet     string
	maxAttempts  int
	store        RandomStore
}

func (s *randomSegment) validate(params map[string]string, now time.Time) error {
	_, err := resolveScopeKey(s.scopeKeyTmpl, s.issuer, s.idType, params, now)
	return err
}

func (s *randomSegment) render(ctx context.Context, params map[string]string, now time.Time) (string, error) {
	key, err := resolveScopeKey(s.scopeKeyTmpl, s.issuer, s.idType, params, now)
	if err != nil {
		return "", err
	}

	for attempt := 0; attempt < s.maxAttempts; attempt++ {
		value, err := randomString(s.alphabet, s.length)
		if err != nil {
			return "", fmt.Errorf("refid: failed to generate random value: %w", err)
		}

		err = s.store.Reserve(ctx, key, value)
		if err == nil {
			return value, nil
		}
		if !errors.Is(err, ErrRandomCollision) {
			return "", err
		}
	}

	return "", fmt.Errorf("%w: scope %q, length %d, charset yielded no free value after %d attempts",
		ErrRandomExhausted, key, s.length, s.maxAttempts)
}

func (s *randomSegment) isStateful() bool { return true }

// randomString returns a random string of length characters drawn uniformly
// from alphabet, using a cryptographically secure random source.
func randomString(alphabet string, length int) (string, error) {
	b := make([]byte, length)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

// newRandomSegment constructs a random segment, associating it with the
// store and binding the issuer/idType from the enclosing format.
func newRandomSegment(cfg SegmentConfig, issuer, idType string, store RandomStore) (*randomSegment, error) {
	if cfg.Random == nil {
		return nil, fmt.Errorf("refid: random segment requires a non-nil random config")
	}
	if cfg.Random.ScopeKey == "" {
		return nil, fmt.Errorf("refid: random segment requires a non-empty scopeKey")
	}
	if store == nil {
		return nil, fmt.Errorf("refid: random segment requires a non-nil RandomStore")
	}
	if cfg.Random.Length < 1 {
		return nil, fmt.Errorf("refid: random segment length must be at least 1, got %d", cfg.Random.Length)
	}
	alphabet, ok := randomAlphabets[cfg.Random.Charset]
	if !ok {
		return nil, fmt.Errorf("refid: random segment has unknown charset %q; must be one of: numeric, alpha, alphanumeric", cfg.Random.Charset)
	}
	if cfg.Random.MaxAttempts < 0 {
		return nil, fmt.Errorf("refid: random segment maxAttempts must not be negative, got %d", cfg.Random.MaxAttempts)
	}
	maxAttempts := cfg.Random.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultRandomMaxAttempts
	}
	return &randomSegment{
		issuer:       issuer,
		idType:       idType,
		scopeKeyTmpl: cfg.Random.ScopeKey,
		length:       cfg.Random.Length,
		alphabet:     alphabet,
		maxAttempts:  maxAttempts,
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
