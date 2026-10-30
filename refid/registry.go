// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package refid provides a shared, config-driven reference ID generation system
// for NSW services.
//
// # Overview
//
// Each ID format is defined as an ordered list of typed segments (literal, list,
// param, date, sequence, random) that are concatenated at generation time.
// Formats are grouped by issuer and identified by an idType, and are looked up
// from a Registry by (issuer, idType). A format may contain at most one
// stateful segment (sequence or random) — see "Stateful segment limit" below.
//
// # Usage
//
//	cfg, err := refid.LoadConfig("refid_config.yaml")
//	store, err := postgres.NewSequence(db) // github.com/OpenNSW/core/refid/store/postgres
//	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(store))
//
//	id, err := reg.Generate(ctx, "RTA", "application_id", map[string]string{
//	    "officeCode": "COL",
//	})
//	// id == "RTA-APP-COL-20260817-000042"
//
// WithSequenceStore and WithRandomStore are both optional — only supply the
// one(s) backing segment types actually used in cfg. If cfg uses random
// segments, also pass a RandomStore:
//
//	randomStore, err := postgres.NewRandom(db)
//	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(store), refid.WithRandomStore(randomStore))
//
// # Config format
//
// See the package-level example_config.yaml for a fully annotated example.
//
// # Scope key placeholders
//
// Sequence and random segments use a scopeKey template to determine the scope
// within which their values must be unique (a counter for sequence segments,
// the set of previously issued values for random segments). Curly braces '{'
// and '}' are reserved in scopeKey templates for placeholder delimiters. The
// following placeholders are resolved at generation time:
//
//	{issuer}    — the issuer identifier for this format
//	{idType}    — the ID type identifier for this format
//	{yyyy}      — four-digit year (in the format's timezone)
//	{yyyyMM}    — year and month (in the format's timezone)
//	{yyyyMMdd}  — year, month, and day (in the format's timezone)
//	{<param>}   — any caller-supplied param not already claimed above
//
// Including {yyyyMMdd} in a scope key gives a daily-resetting counter (or
// daily-reset uniqueness set, for random segments); omitting all date
// components gives a scope that never resets.
//
// # Date formats
//
// A date segment's format is a pattern of letters where a repeated letter means
// zero padding: "yyyyMMdd" renders 20260305 and "yyyyMd" renders 202635. The
// fields are:
//
//	yyyy, yy   year (2026, 26)
//	MM, M      month (03, 3)
//	dd, d      day of the month (05, 5)
//	HH, H      hour, 0-23 (04, 4)
//	mm, m      minute (07, 7)
//	ss, s      second (09, 9)
//
// Text in single quotes is literal ("yyyyMMdd'T'HHmm" renders 20260305T0407, and
// a doubled single quote is an apostrophe), as is any other character that is
// not an ASCII letter or digit ("yyyy-MM-dd", "d/M/yyyy"). NewRegistry rejects what it does not
// understand: unknown letters, a run of letters that is not a form above
// ("yyy", "MMM"), unquoted digits (which is what a Go reference layout such as
// "20060102" looks like), an unterminated quote, and a format with no field.
// Unpadded fields make IDs variable-width: "yyyyMd" gives 2026111 for both 11
// January and 1 November.
//
// # Timezone
//
// Each format reads the clock in its own timezone, an IANA time zone name set
// by FormatConfig.Timezone. The same zone drives the date segments and the
// date placeholders above, so the date in an ID and the point where its
// counter resets always agree: for "Asia/Colombo" (UTC+5:30) a daily counter
// resets at local midnight, not at 05:30.
//
//	formats:
//	  - idType: sample_reference_no
//	    timezone: Asia/Colombo
//	    segments:
//	      - type: date
//	        format: "yyyy/MM/"
//	      - type: sequence
//	        sequence:
//	          scopeKey: "{issuer}:{idType}:{yyyy}"
//	          padding: 4
//
// Timezone is required for a format with a date segment or a sequence or
// random scope key containing {yyyy}, {yyyyMM} or {yyyyMMdd}, and optional
// otherwise. NewRegistry rejects a missing, unknown or "Local" zone (the
// latter would tie IDs to the host's time zone). Zone names are resolved with
// time.LoadLocation, so the host needs a time zone database; a binary can
// embed one by importing time/tzdata or building with -tags timetzdata.
//
// # Counter overflow
//
// If a counter exceeds the maximum value representable with the configured
// padding width, Generate returns ErrCounterOverflow. This is intentional: a
// wider-than-expected ID would silently break any downstream system that
// validates ID length. Operations should be alerted and the scope key
// configuration reviewed.
//
// # Random segment exhaustion
//
// A random segment generates a value from its charset/length and reserves it
// via RandomStore, retrying on collision up to maxAttempts (default 10). If
// every attempt collides, Generate returns ErrRandomExhausted — this means
// the charset/length combination is too small for the number of values
// already issued in that scope; widen the charset or length, or narrow the
// scope key (e.g. add {yyyyMMdd}).
//
// # Stateful segment limit
//
// Generate has no transaction or rollback across segments: it validates all
// segments, then renders them in order, and a sequence or random segment's
// store call (a counter increment, a random value reservation) takes effect
// immediately as a side effect of rendering. If a format had two or more
// stateful segments and a later one failed during render (e.g. a sequence
// segment overflowing, or a random segment exhausting its retries), an
// earlier stateful segment's already-committed side effect would be
// permanently orphaned — for a random segment, that permanently wastes one
// value from its bounded charset/length space for no returned ID. To rule
// this out, NewRegistry rejects any format with more than one sequence or
// random segment combined.
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
	// params supplies caller-provided values consumed by list, param, sequence,
	// and random segments (e.g. map[string]string{"officeCode": "COL"}). Unused
	// keys are silently ignored; missing required keys return ErrInvalidParam.
	//
	// Errors:
	//   - ErrUnknownIssuer   — issuer not found in config
	//   - ErrUnknownIDType   — idType not found under the given issuer
	//   - ErrInvalidParam    — a required param is missing or has an invalid value
	//   - ErrCounterOverflow — sequence counter exceeds padding width
	//   - ErrRandomExhausted — random segment found no free value within maxAttempts
	Generate(ctx context.Context, issuer, idType string, params map[string]string) (string, error)
}

// RegistryOption configures optional behavior for NewRegistry.
type RegistryOption func(*registryConfig)

type registryConfig struct {
	sequenceStore SequenceStore
	randomStore   RandomStore
}

// WithSequenceStore supplies the SequenceStore used to back "sequence"
// segments. It is only required if the config contains at least one sequence
// segment; NewRegistry returns an error when called if one is used without
// this option set.
func WithSequenceStore(store SequenceStore) RegistryOption {
	return func(rc *registryConfig) {
		rc.sequenceStore = store
	}
}

// WithRandomStore supplies the RandomStore used to back "random" segments.
// It is only required if the config contains at least one random segment;
// NewRegistry returns an error when called if one is used without this
// option set.
func WithRandomStore(store RandomStore) RegistryOption {
	return func(rc *registryConfig) {
		rc.randomStore = store
	}
}

// formatKey is the composite map key used to look up a pre-compiled format.
type formatKey struct {
	issuer string
	idType string
}

// compiledFormat is a pre-validated, ready-to-render list of segments.
type compiledFormat struct {
	segments []segment

	// location is the timezone the format reads the clock in. It is time.UTC
	// for a format that has no date use and so no configured timezone.
	location *time.Location
}

// registry is the package-private implementation of Registry.
// It is read-only after construction; no locking is needed here.
// Concurrent safety for sequence counters is the responsibility of SequenceStore.
type registry struct {
	formats map[formatKey]*compiledFormat

	// now returns the current time. It is time.Now except in tests, which
	// replace it to pin the instant.
	now func() time.Time
}

// NewRegistry validates cfg and compiles all formats into an internal lookup
// table. It returns an error if the config contains:
//   - duplicate (issuer, idType) pairs
//   - a segment that references an undefined list name
//   - a segment with missing required fields (e.g. empty scopeKey, empty format)
//   - a date segment whose format is not valid (see "Date formats")
//   - an unrecognised segment type
//   - more than one stateful (sequence or random) segment in a single format
//   - a format with a date segment or a date placeholder in a scope key but no
//     timezone, or any timezone that is unknown or "Local"
//
// Fail-fast at startup: every error that would surface at generation time is
// caught here instead.
func NewRegistry(cfg Config, opts ...RegistryOption) (Registry, error) {
	var rc registryConfig
	for _, opt := range opts {
		opt(&rc)
	}

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

			compiled, err := compileFormat(fmtCfg, issuerCfg.Issuer, cfg.Lists, rc.sequenceStore, rc.randomStore)
			if err != nil {
				return nil, fmt.Errorf("refid: compiling format (%q, %q): %w", issuerCfg.Issuer, fmtCfg.IDType, err)
			}

			formats[key] = compiled
		}
	}

	return &registry{formats: formats, now: time.Now}, nil
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

	// Every segment and scope key in this call reads this one instant, in the
	// format's own timezone, so the date in the ID and the counter's scope
	// cannot disagree.
	now := r.now().In(compiled.location)

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
func compileFormat(cfg FormatConfig, issuer string, lists map[string][]string, store SequenceStore, randomStore RandomStore) (*compiledFormat, error) {
	if len(cfg.Segments) == 0 {
		return nil, fmt.Errorf("format has no segments")
	}

	segs := make([]segment, 0, len(cfg.Segments))
	statefulCount := 0
	for i, sc := range cfg.Segments {
		seg, err := compileSegment(sc, issuer, cfg.IDType, lists, store, randomStore)
		if err != nil {
			return nil, fmt.Errorf("segment[%d] (type=%q): %w", i, sc.Type, err)
		}
		if seg.isStateful() {
			statefulCount++
		}
		segs = append(segs, seg)
	}

	// A format may contain at most one stateful segment (sequence or random).
	// Generate renders segments in order with no rollback: if a stateful
	// segment's store call commits (a counter increment, a random value
	// reservation) and a later segment then fails, the earlier side effect
	// is permanently orphaned — for a random segment this permanently wastes
	// one value from its bounded charset/length space. Capping formats at one
	// stateful segment rules this out entirely, since every other segment
	// type is a pure function of (params, now) and cannot fail during render
	// once the validation pass has already succeeded. See segment.isStateful.
	if statefulCount > 1 {
		return nil, fmt.Errorf("format has %d stateful (sequence/random) segments; at most 1 is allowed per format", statefulCount)
	}

	location, err := resolveTimezone(cfg)
	if err != nil {
		return nil, err
	}

	return &compiledFormat{segments: segs, location: location}, nil
}

// resolveTimezone returns the location a format reads the clock in. A timezone
// is required only when the format uses a date (see formatUsesDate); a format
// without one reads the clock in UTC, which no segment or scope key can observe.
// A timezone that is set is validated either way, so a typo can't pass silently.
func resolveTimezone(cfg FormatConfig) (*time.Location, error) {
	if cfg.Timezone == "" {
		if formatUsesDate(cfg) {
			return nil, fmt.Errorf("timezone is required because the format has a date segment or a date placeholder ({yyyy}, {yyyyMM}, {yyyyMMdd}) in a scope key; set an IANA zone name such as \"Asia/Colombo\" or \"UTC\"")
		}
		return time.UTC, nil
	}

	// time.LoadLocation resolves "Local" to the host's zone, which would make
	// the same config render different IDs on differently configured hosts.
	if cfg.Timezone == "Local" {
		return nil, fmt.Errorf("timezone %q is not allowed because it depends on the host; use an IANA zone name such as \"Asia/Colombo\" or \"UTC\"", cfg.Timezone)
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("timezone %q is not a known IANA zone name: %w", cfg.Timezone, err)
	}
	return loc, nil
}

// formatUsesDate reports whether rendering the format reads the date: it has a
// date segment, or a sequence or random segment whose scope key contains a date
// placeholder.
func formatUsesDate(cfg FormatConfig) bool {
	for _, sc := range cfg.Segments {
		switch sc.Type {
		case SegmentTypeDate:
			return true
		case SegmentTypeSequence:
			if sc.Sequence != nil && scopeKeyUsesDate(sc.Sequence.ScopeKey) {
				return true
			}
		case SegmentTypeRandom:
			if sc.Random != nil && scopeKeyUsesDate(sc.Random.ScopeKey) {
				return true
			}
		}
	}
	return false
}

// compileSegment dispatches to the appropriate constructor based on sc.Type.
func compileSegment(sc SegmentConfig, issuer, idType string, lists map[string][]string, store SequenceStore, randomStore RandomStore) (segment, error) {
	switch sc.Type {
	case SegmentTypeLiteral:
		return newLiteralSegment(sc)

	case SegmentTypeList:
		values, ok := lists[sc.List]
		if !ok {
			return nil, fmt.Errorf("list %q is not defined in config", sc.List)
		}
		return newListSegment(sc, values)

	case SegmentTypeParam:
		return newParamSegment(sc)

	case SegmentTypeDate:
		return newDateSegment(sc)

	case SegmentTypeSequence:
		return newSequenceSegment(sc, issuer, idType, store)

	case SegmentTypeRandom:
		return newRandomSegment(sc, issuer, idType, randomStore)

	default:
		return nil, fmt.Errorf("unknown segment type %q; must be one of: %s, %s, %s, %s, %s, %s",
			sc.Type, SegmentTypeLiteral, SegmentTypeList, SegmentTypeParam, SegmentTypeDate, SegmentTypeSequence, SegmentTypeRandom)
	}
}
