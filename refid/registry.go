// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package refid provides a shared, config-driven reference ID generation system
// for NSW services.
//
// # Overview
//
// Each ID format is defined as an ordered list of typed segments (literal, list,
// date, sequence) that are concatenated at generation time. Formats are grouped
// by issuer and identified by an idType, and are looked up from a Registry by
// (issuer, idType).
//
// # Usage
//
//	cfg, err := refid.LoadConfig("refid_config.yaml")
//	store := refid.NewPostgresStore(db)
//	reg, err := refid.NewRegistry(cfg, store)
//
//	id, err := reg.Generate(ctx, "RTA", "application_id", map[string]string{
//	    "officeCode": "COL",
//	})
//	// id == "RTA-APP-COL-20260817-000042"
//
// # Config format
//
// See the package-level example_config.yaml for a fully annotated example.
//
// # Scope key placeholders
//
// Sequence segments use a scopeKey template to determine counter scope.
// Curly braces '{' and '}' are reserved in scopeKey templates for placeholder
// delimiters. The following placeholders are resolved at generation time:
//
//	{issuer}    — the issuer identifier for this format
//	{idType}    — the ID type identifier for this format
//	{yyyy}      — four-digit year (UTC)
//	{yyyyMM}    — year and month (UTC)
//	{yyyyMMdd}  — year, month, and day (UTC)
//	{<param>}   — any caller-supplied param not already claimed above
//
// Including {yyyyMMdd} in a scope key gives a daily-resetting counter; omitting
// all date components gives a counter that never resets.
//
// # Counter overflow
//
// If a counter exceeds the maximum value representable with the configured
// padding width, Generate returns ErrCounterOverflow. This is intentional: a
// wider-than-expected ID would silently break any downstream system that
// validates ID length. Operations should be alerted and the scope key
// configuration reviewed.
package refid

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Registry is the entry point for generating IDs. Obtain one via NewRegistry.
//
// Implementations must be safe for concurrent use by multiple goroutines.
type Registry interface {
	// Generate produces a new ID for the given issuer and idType.
	//
	// params supplies caller-provided values consumed by list and sequence
	// segments (e.g. map[string]string{"officeCode": "COL"}). Unused keys are
	// silently ignored; missing required keys return ErrInvalidParam.
	//
	// Errors:
	//   - ErrUnknownIssuer   — issuer not found in config
	//   - ErrUnknownIDType   — idType not found under the given issuer
	//   - ErrInvalidParam    — a required param is missing or has an invalid value
	//   - ErrCounterOverflow — sequence counter exceeds padding width
	Generate(ctx context.Context, issuer, idType string, params map[string]string) (string, error)
}

// formatKey is the composite map key used to look up a pre-compiled format.
type formatKey struct {
	issuer string
	idType string
}

// compiledFormat is a pre-validated, ready-to-render list of segments.
type compiledFormat struct {
	segments []segment
}

// registry is the package-private implementation of Registry.
// It is read-only after construction; no locking is needed here.
// Concurrent safety for sequence counters is the responsibility of SequenceStore.
type registry struct {
	formats map[formatKey]*compiledFormat
}

// NewRegistry validates cfg and compiles all formats into an internal lookup
// table. It returns an error if the config contains:
//   - duplicate (issuer, idType) pairs
//   - a segment that references an undefined list name
//   - a segment with missing required fields (e.g. empty scopeKey, empty layout)
//   - an unrecognised segment type
//
// Fail-fast at startup: every error that would surface at generation time is
// caught here instead.
func NewRegistry(cfg Config, store SequenceStore) (Registry, error) {
	formats := make(map[formatKey]*compiledFormat)

	for _, issuerCfg := range cfg.Issuers {
		if issuerCfg.Issuer == "" {
			return nil, fmt.Errorf("refid: issuer config has an empty issuer name")
		}

		for _, fmtCfg := range issuerCfg.Formats {
			if fmtCfg.IDType == "" {
				return nil, fmt.Errorf("refid: issuer %q has a format with an empty idType", issuerCfg.Issuer)
			}

			key := formatKey{issuer: issuerCfg.Issuer, idType: fmtCfg.IDType}
			if _, exists := formats[key]; exists {
				return nil, fmt.Errorf("refid: duplicate format (%q, %q)", issuerCfg.Issuer, fmtCfg.IDType)
			}

			compiled, err := compileFormat(fmtCfg, issuerCfg.Issuer, cfg.Lists, store)
			if err != nil {
				return nil, fmt.Errorf("refid: compiling format (%q, %q): %w", issuerCfg.Issuer, fmtCfg.IDType, err)
			}

			formats[key] = compiled
		}
	}

	return &registry{formats: formats}, nil
}

// Generate implements Registry.
func (r *registry) Generate(ctx context.Context, issuer, idType string, params map[string]string) (string, error) {
	key := formatKey{issuer: issuer, idType: idType}
	compiled, ok := r.formats[key]
	if !ok {
		// Distinguish between unknown issuer vs unknown idType for clearer error messages.
		for k := range r.formats {
			if k.issuer == issuer {
				return "", fmt.Errorf("%w: issuer=%q idType=%q", ErrUnknownIDType, issuer, idType)
			}
		}
		return "", fmt.Errorf("%w: %q", ErrUnknownIssuer, issuer)
	}

	now := time.Now().UTC()

	// 1. Validation pass: validate all segments before executing any side-effects (e.g. sequence counter increments)
	for _, seg := range compiled.segments {
		if err := seg.validate(params, now); err != nil {
			return "", err
		}
	}

	// 2. Render pass: execute rendering (sequence store increments happen here)
	var sb strings.Builder
	for _, seg := range compiled.segments {
		part, err := seg.render(ctx, params, now)
		if err != nil {
			return "", err
		}
		sb.WriteString(part)
	}
	return sb.String(), nil
}

// compileFormat validates and pre-compiles a single FormatConfig into its
// segment implementations.
func compileFormat(cfg FormatConfig, issuer string, lists map[string][]string, store SequenceStore) (*compiledFormat, error) {
	if len(cfg.Segments) == 0 {
		return nil, fmt.Errorf("format has no segments")
	}

	segs := make([]segment, 0, len(cfg.Segments))
	for i, sc := range cfg.Segments {
		seg, err := compileSegment(sc, issuer, cfg.IDType, lists, store)
		if err != nil {
			return nil, fmt.Errorf("segment[%d] (type=%q): %w", i, sc.Type, err)
		}
		segs = append(segs, seg)
	}
	return &compiledFormat{segments: segs}, nil
}

// compileSegment dispatches to the appropriate constructor based on sc.Type.
func compileSegment(sc SegmentConfig, issuer, idType string, lists map[string][]string, store SequenceStore) (segment, error) {
	switch sc.Type {
	case "literal":
		return newLiteralSegment(sc)

	case "list":
		values, ok := lists[sc.List]
		if !ok {
			return nil, fmt.Errorf("list %q is not defined in config", sc.List)
		}
		return newListSegment(sc, values)

	case "date":
		return newDateSegment(sc)

	case "sequence":
		return newSequenceSegment(sc, issuer, idType, store)

	default:
		return nil, fmt.Errorf("unknown segment type %q; must be one of: literal, list, date, sequence", sc.Type)
	}
}
