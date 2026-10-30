// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

// These tests live in package refid so they can call the unexported helpers
// (compileFormat, resolveScopeKey, scopeKeyUsesDate). They pin the instant
// with WithClock to check which date each timezone renders and which scope key
// it counts under. Config validation and the clock option are tested from
// outside the package in refid_test.go.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// recSeqStore is a SequenceStore that counts per scope key and records every
// scope key it was asked for, in call order.
type recSeqStore struct {
	counters map[string]int64
	keys     []string
}

func newRecSeqStore() *recSeqStore {
	return &recSeqStore{counters: make(map[string]int64)}
}

func (s *recSeqStore) Next(_ context.Context, scopeKey string, max int64) (int64, error) {
	s.keys = append(s.keys, scopeKey)
	if s.counters[scopeKey] >= max {
		return 0, ErrCounterOverflow
	}
	s.counters[scopeKey]++
	return s.counters[scopeKey], nil
}

// recRandStore is a RandomStore that accepts every value and records the scope
// keys it was asked to reserve under.
type recRandStore struct {
	keys []string
}

func (s *recRandStore) Reserve(_ context.Context, scopeKey, _ string) error {
	s.keys = append(s.keys, scopeKey)
	return nil
}

func instant(t *testing.T, rfc3339 string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// newPinned builds a registry from cfg whose clock reads *now, so a test can
// move time by assigning to it.
func newPinned(t *testing.T, cfg Config, now *time.Time, opts ...RegistryOption) Registry {
	t.Helper()
	opts = append(opts[:len(opts):len(opts)], WithClock(func() time.Time { return *now }))
	reg, err := NewRegistry(cfg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func generate(t *testing.T, reg Registry, issuer, idType string) string {
	t.Helper()
	id, err := reg.Generate(context.Background(), issuer, idType, nil)
	if err != nil {
		t.Fatalf("Generate(%q, %q): %v", issuer, idType, err)
	}
	return id
}

func dateSeg(format string) SegmentConfig {
	return SegmentConfig{Type: SegmentTypeDate, Format: format}
}

func literalSeg(value string) SegmentConfig {
	return SegmentConfig{Type: SegmentTypeLiteral, Value: value}
}

func seqSeg(scopeKey string) SegmentConfig {
	return SegmentConfig{Type: SegmentTypeSequence, Sequence: &SequenceSegmentConfig{ScopeKey: scopeKey, Padding: 4}}
}

// oneFormat is a config whose only format is (ACME, f.IDType).
func oneFormat(f FormatConfig) Config {
	return Config{Issuers: []IssuerConfig{{Issuer: "ACME", Formats: []FormatConfig{f}}}}
}

// sampleReference is the format from the issue: a year and month followed by a
// 4-digit sequence that restarts every year.
func sampleReference(timezone string) FormatConfig {
	return FormatConfig{
		IDType:   "sample_reference_no",
		Timezone: timezone,
		Segments: []SegmentConfig{dateSeg("yyyy/MM/"), seqSeg("{issuer}:{idType}:{yyyy}")},
	}
}

// scopeKeysAt generates one ID at each of two instants from a sequence-only
// format scoped by scopeKey in timezone, and returns the scope key each call
// counted under.
func scopeKeysAt(t *testing.T, timezone, scopeKey, first, second string) (string, string) {
	t.Helper()
	store := newRecSeqStore()
	now := instant(t, first)
	reg := newPinned(t, oneFormat(FormatConfig{
		IDType:   "ref",
		Timezone: timezone,
		Segments: []SegmentConfig{seqSeg(scopeKey)},
	}), &now, WithSequenceStore(store))

	generate(t, reg, "ACME", "ref")
	now = instant(t, second)
	generate(t, reg, "ACME", "ref")
	return store.keys[0], store.keys[1]
}

// 20:00 UTC on 31 December is 01:30 on 1 January in Colombo, so the ID and the
// yearly counter both belong to the new year there and to the old one in UTC.
func TestGenerate_Timezone_YearBoundary(t *testing.T) {
	tests := []struct {
		timezone  string
		wantID    string
		wantScope string
	}{
		{"UTC", "2026/12/0001", "ACME:sample_reference_no:2026"},
		{"Asia/Colombo", "2027/01/0001", "ACME:sample_reference_no:2027"},
		{"America/New_York", "2026/12/0001", "ACME:sample_reference_no:2026"}, // 15:00 on 31 December
		{"Pacific/Kiritimati", "2027/01/0001", "ACME:sample_reference_no:2027"},
	}
	for _, tt := range tests {
		t.Run(tt.timezone, func(t *testing.T) {
			store := newRecSeqStore()
			now := instant(t, "2026-12-31T20:00:00Z")
			reg := newPinned(t, oneFormat(sampleReference(tt.timezone)), &now, WithSequenceStore(store))

			if got := generate(t, reg, "ACME", "sample_reference_no"); got != tt.wantID {
				t.Errorf("ID = %q, want %q", got, tt.wantID)
			}
			if got := store.keys[0]; got != tt.wantScope {
				t.Errorf("scope key = %q, want %q", got, tt.wantScope)
			}
		})
	}
}

// Midnight in Colombo is 18:30 UTC. The year in the ID and the counter must turn
// over at that instant together, and nothing more happens at the UTC midnight
// (05:30 in Colombo) where the old behaviour reset.
func TestGenerate_Timezone_CounterResetsAtLocalMidnight(t *testing.T) {
	store := newRecSeqStore()
	now := instant(t, "2026-12-31T18:29:59Z")
	reg := newPinned(t, oneFormat(sampleReference("Asia/Colombo")), &now, WithSequenceStore(store))

	steps := []struct {
		at   string
		want string
	}{
		{"2026-12-31T18:29:59Z", "2026/12/0001"}, // 23:59:59 in Colombo
		{"2026-12-31T18:29:59Z", "2026/12/0002"},
		{"2026-12-31T18:30:00Z", "2027/01/0001"}, // midnight in Colombo
		{"2026-12-31T18:30:01Z", "2027/01/0002"},
		{"2027-01-01T00:00:00Z", "2027/01/0003"}, // UTC midnight, 05:30 in Colombo: no reset
	}
	for _, step := range steps {
		now = instant(t, step.at)
		if got := generate(t, reg, "ACME", "sample_reference_no"); got != step.want {
			t.Errorf("at %s: ID = %q, want %q", step.at, got, step.want)
		}
	}

	if got := store.counters["ACME:sample_reference_no:2026"]; got != 2 {
		t.Errorf("2026 counter = %d, want 2 (untouched after the reset)", got)
	}
	if got := store.counters["ACME:sample_reference_no:2027"]; got != 3 {
		t.Errorf("2027 counter = %d, want 3", got)
	}
}

func TestGenerate_Timezone_DateScopeKeyRollsAtLocalMidnight(t *testing.T) {
	tests := []struct {
		name         string
		timezone     string
		scopeKey     string
		first        string
		second       string
		wantFirst    string
		wantSecond   string
		wantSameKeys bool
	}{
		{"daily in Colombo", "Asia/Colombo", "{issuer}:{idType}:{yyyyMMdd}", "2026-06-14T18:29:59Z", "2026-06-14T18:30:00Z", "ACME:ref:20260614", "ACME:ref:20260615", false},
		{"daily in Colombo, not at 05:30 UTC", "Asia/Colombo", "{issuer}:{idType}:{yyyyMMdd}", "2026-06-14T05:29:59Z", "2026-06-14T05:30:00Z", "ACME:ref:20260614", "ACME:ref:20260614", true},
		{"monthly in Colombo", "Asia/Colombo", "{issuer}:{idType}:{yyyyMM}", "2026-01-31T18:29:59Z", "2026-01-31T18:30:00Z", "ACME:ref:202601", "ACME:ref:202602", false},
		{"yearly in Colombo", "Asia/Colombo", "{issuer}:{idType}:{yyyy}", "2026-12-31T18:29:59Z", "2026-12-31T18:30:00Z", "ACME:ref:2026", "ACME:ref:2027", false},
		{"daily in UTC", "UTC", "{issuer}:{idType}:{yyyyMMdd}", "2026-06-14T23:59:59Z", "2026-06-15T00:00:00Z", "ACME:ref:20260614", "ACME:ref:20260615", false},
		{"daily west of UTC", "America/Los_Angeles", "{issuer}:{idType}:{yyyyMMdd}", "2026-06-15T06:59:59Z", "2026-06-15T07:00:00Z", "ACME:ref:20260614", "ACME:ref:20260615", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, second := scopeKeysAt(t, tt.timezone, tt.scopeKey, tt.first, tt.second)
			if first != tt.wantFirst || second != tt.wantSecond {
				t.Errorf("scope keys = %q, %q; want %q, %q", first, second, tt.wantFirst, tt.wantSecond)
			}
			if (first == second) != tt.wantSameKeys {
				t.Errorf("same scope = %t, want %t", first == second, tt.wantSameKeys)
			}
		})
	}
}

// A zone with daylight saving has local midnights that move relative to UTC,
// and 23- and 25-hour days. A fixed offset would roll at the wrong instant.
func TestGenerate_Timezone_FollowsDaylightSavingRules(t *testing.T) {
	const daily = "{issuer}:{idType}:{yyyyMMdd}"
	tests := []struct {
		name       string
		scopeKey   string
		first      string
		second     string
		wantFirst  string
		wantSecond string
	}{
		{"new year, UTC-5", "{issuer}:{idType}:{yyyy}", "2027-01-01T04:59:59Z", "2027-01-01T05:00:00Z", "ACME:ref:2026", "ACME:ref:2027"},
		{"midnight before fall-back, UTC-4", daily, "2026-11-01T03:59:59Z", "2026-11-01T04:00:00Z", "ACME:ref:20261031", "ACME:ref:20261101"},
		{"midnight after fall-back, UTC-5", daily, "2026-11-02T04:59:59Z", "2026-11-02T05:00:00Z", "ACME:ref:20261101", "ACME:ref:20261102"},      // 1 November lasts 25 hours
		{"midnight after spring-forward, UTC-4", daily, "2026-03-09T03:59:59Z", "2026-03-09T04:00:00Z", "ACME:ref:20260308", "ACME:ref:20260309"}, // 8 March lasts 23 hours
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, second := scopeKeysAt(t, "America/New_York", tt.scopeKey, tt.first, tt.second)
			if first != tt.wantFirst || second != tt.wantSecond {
				t.Errorf("scope keys = %q, %q; want %q, %q", first, second, tt.wantFirst, tt.wantSecond)
			}
		})
	}
}

// Across zones and instants that straddle year, month and DST changes, the date
// in the ID, the date in the scope key and the zone's own calendar all agree.
func TestGenerate_Timezone_IDDateAndScopeKeyAgree(t *testing.T) {
	zones := []string{"UTC", "Asia/Colombo", "America/New_York", "Pacific/Kiritimati", "Pacific/Pago_Pago"}
	starts := []string{"2026-12-31T00:00:00Z", "2026-02-28T00:00:00Z", "2026-11-01T00:00:00Z"}

	for _, zone := range zones {
		for _, start := range starts {
			t.Run(zone+"/"+start, func(t *testing.T) {
				loc := mustLocation(t, zone)
				store := newRecSeqStore()
				now := instant(t, start)
				reg := newPinned(t, oneFormat(FormatConfig{
					IDType:   "ref",
					Timezone: zone,
					Segments: []SegmentConfig{dateSeg("yyyyMMdd"), literalSeg("-"), seqSeg("{issuer}:{idType}:{yyyyMMdd}")},
				}), &now, WithSequenceStore(store))

				base := now
				for i := 0; i <= 96; i++ { // 48 hours in half-hour steps
					now = base.Add(time.Duration(i) * 30 * time.Minute)
					id := generate(t, reg, "ACME", "ref")

					want := now.In(loc).Format("20060102")
					idDate, _, _ := strings.Cut(id, "-")
					scopeDate := strings.TrimPrefix(store.keys[len(store.keys)-1], "ACME:ref:")
					if idDate != want || scopeDate != want {
						t.Fatalf("at %s: ID date %q, scope key date %q, want both %q", now.Format(time.RFC3339), idDate, scopeDate, want)
					}
				}
			})
		}
	}
}

// A date-only format has no stateful segment, so it needs no store, and its
// format can show the time of day as well as the date, padded or not.
func TestGenerate_Timezone_DateSegmentFormats(t *testing.T) {
	const at = "2026-12-31T20:00:00Z"
	tests := []struct {
		format   string
		timezone string
		want     string
	}{
		{"yyyyMMdd", "UTC", "20261231"},
		{"yyyyMMdd", "Asia/Colombo", "20270101"},
		{"yyyyMMdd'T'HHmm", "UTC", "20261231T2000"},
		{"yyyyMMdd'T'HHmm", "Asia/Colombo", "20270101T0130"},
		{"yyyy-MM-dd'T'HH:mm", "Asia/Colombo", "2027-01-01T01:30"},
		{"yyyy-MM-dd'T'HH:mm", "America/New_York", "2026-12-31T15:00"},
		{"d/M/yyyy H:m", "Asia/Colombo", "1/1/2027 1:30"},
		{"d/M/yyyy H:m", "America/New_York", "31/12/2026 15:0"},
	}
	for _, tt := range tests {
		t.Run(tt.timezone+" "+tt.format, func(t *testing.T) {
			now := instant(t, at)
			reg := newPinned(t, oneFormat(FormatConfig{
				IDType:   "stamp",
				Timezone: tt.timezone,
				Segments: []SegmentConfig{dateSeg(tt.format)},
			}), &now)

			if got := generate(t, reg, "ACME", "stamp"); got != tt.want {
				t.Errorf("ID = %q, want %q", got, tt.want)
			}
		})
	}
}

// Each idType reads the clock in its own zone, and a format that reads no date
// is unaffected by the others' zones or by the clock.
func TestGenerate_Timezone_PerIDType(t *testing.T) {
	store := newRecSeqStore()
	cfg := Config{Issuers: []IssuerConfig{{
		Issuer: "ACME",
		Formats: []FormatConfig{
			{IDType: "colombo_day", Timezone: "Asia/Colombo", Segments: []SegmentConfig{dateSeg("yyyyMMdd")}},
			{IDType: "utc_day", Timezone: "UTC", Segments: []SegmentConfig{dateSeg("yyyyMMdd")}},
			{IDType: "fixed", Segments: []SegmentConfig{literalSeg("FIXED")}},
			{IDType: "permit", Segments: []SegmentConfig{seqSeg("{issuer}:{idType}")}},
		},
	}}}
	now := instant(t, "2026-12-31T20:00:00Z")
	reg := newPinned(t, cfg, &now, WithSequenceStore(store))

	for idType, want := range map[string]string{
		"colombo_day": "20270101",
		"utc_day":     "20261231",
		"fixed":       "FIXED",
		"permit":      "0001",
	} {
		if got := generate(t, reg, "ACME", idType); got != want {
			t.Errorf("%s = %q, want %q", idType, got, want)
		}
	}

	// Months later the permit counter, which never resets, carries on.
	now = instant(t, "2027-06-01T00:00:00Z")
	if got := generate(t, reg, "ACME", "permit"); got != "0002" {
		t.Errorf("permit = %q, want 0002", got)
	}
	if got := store.keys[len(store.keys)-1]; got != "ACME:permit" {
		t.Errorf("permit scope key = %q, want ACME:permit", got)
	}
}

// A random segment's uniqueness scope follows the zone the same way a
// sequence's counter does.
func TestGenerate_Timezone_RandomScopeKey(t *testing.T) {
	tests := []struct {
		timezone string
		want     string
	}{
		{"UTC", "ACME:token:20261231"},
		{"Asia/Colombo", "ACME:token:20270101"},
	}
	for _, tt := range tests {
		t.Run(tt.timezone, func(t *testing.T) {
			store := &recRandStore{}
			now := instant(t, "2026-12-31T20:00:00Z")
			reg := newPinned(t, oneFormat(FormatConfig{
				IDType:   "token",
				Timezone: tt.timezone,
				Segments: []SegmentConfig{{
					Type:   SegmentTypeRandom,
					Random: &RandomSegmentConfig{ScopeKey: "{issuer}:{idType}:{yyyyMMdd}", Charset: CharsetNumeric, Length: 4},
				}},
			}), &now, WithRandomStore(store))

			generate(t, reg, "ACME", "token")
			if len(store.keys) != 1 || store.keys[0] != tt.want {
				t.Errorf("reserved under %q, want [%q]", store.keys, tt.want)
			}
		})
	}
}

// Without a test override the registry reads the real clock. The format shows
// the time of day, and Colombo is always 5 hours 30 minutes off UTC, so the zone
// is visible at any time of day.
func TestGenerate_Timezone_DefaultClockIsWallClock(t *testing.T) {
	const goLayout = "2006-01-02T15:04" // the same fields as the format below
	loc := mustLocation(t, "Asia/Colombo")
	reg, err := NewRegistry(oneFormat(FormatConfig{
		IDType:   "stamp",
		Timezone: "Asia/Colombo",
		Segments: []SegmentConfig{dateSeg("yyyy-MM-dd'T'HH:mm")},
	}))
	if err != nil {
		t.Fatal(err)
	}

	before := time.Now().In(loc).Format(goLayout)
	id := generate(t, reg, "ACME", "stamp")
	after := time.Now().In(loc).Format(goLayout)

	if id != before && id != after {
		t.Errorf("ID = %q, want the Colombo time between %q and %q", id, before, after)
	}
	if utc := time.Now().UTC().Format(goLayout); id == utc {
		t.Errorf("ID = %q is the UTC time, want the Colombo time", id)
	}
}

func TestScopeKeyUsesDate(t *testing.T) {
	tests := []struct {
		tmpl string
		want bool
	}{
		{"", false},
		{"{issuer}:{idType}", false},
		{"{issuer}:{idType}:{officeCode}", false},
		{"{yyyy}", true},
		{"{yyyyMM}", true},
		{"{yyyyMMdd}", true},
		{"{issuer}:{idType}:{officeCode}:{yyyyMMdd}", true},
		{"{yyyyy}", false},
		{"{yyyyMMddd}", false},
		{"{YYYY}", false},
		{"yyyy", false},
	}
	for _, tt := range tests {
		if got := scopeKeyUsesDate(tt.tmpl); got != tt.want {
			t.Errorf("scopeKeyUsesDate(%q) = %t, want %t", tt.tmpl, got, tt.want)
		}
	}
}

func TestResolveScopeKey_DateTokensUseTheGivenZone(t *testing.T) {
	now := instant(t, "2026-12-31T20:00:00Z").In(mustLocation(t, "Asia/Colombo"))

	got, err := resolveScopeKey("{yyyy}|{yyyyMM}|{yyyyMMdd}", "ACME", "ref", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := "2027|202701|20270101"; got != want {
		t.Errorf("resolveScopeKey = %q, want %q", got, want)
	}
}

// A format that reads no date is compiled with UTC rather than a nil location,
// since Generate converts the clock to the location on every call.
func TestCompileFormat_LocationWithoutDateUse(t *testing.T) {
	compiled, err := compileFormat(FormatConfig{
		IDType:   "fixed",
		Segments: []SegmentConfig{literalSeg("FIXED")},
	}, "ACME", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.location != time.UTC {
		t.Errorf("location = %v, want UTC", compiled.location)
	}

	compiled, err = compileFormat(sampleReference("Asia/Colombo"), "ACME", nil, newRecSeqStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := compiled.location.String(); got != "Asia/Colombo" {
		t.Errorf("location = %q, want Asia/Colombo", got)
	}
}
