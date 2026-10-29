// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenNSW/core/refid"
)

// -----------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------

// memStore is an in-memory SequenceStore for unit tests.
// It is safe for concurrent use.
type memStore struct {
	mu       sync.Mutex
	counters map[string]int64
}

func newMemStore() *memStore {
	return &memStore{counters: make(map[string]int64)}
}

func (m *memStore) Next(_ context.Context, scopeKey string, max int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counters[scopeKey] >= max {
		return 0, refid.ErrCounterOverflow
	}
	m.counters[scopeKey]++
	return m.counters[scopeKey], nil
}

// fixedStore always returns a fixed counter value, regardless of scope key.
// Used to test overflow detection.
type fixedStore struct {
	value int64
}

func (f *fixedStore) Next(_ context.Context, _ string, max int64) (int64, error) {
	if f.value > max {
		return 0, refid.ErrCounterOverflow
	}
	return f.value, nil
}

// memRandomStore is an in-memory RandomStore for unit tests. It is safe for
// concurrent use.
type memRandomStore struct {
	mu       sync.Mutex
	reserved map[string]map[string]struct{} // scopeKey -> set of reserved values
}

func newMemRandomStore() *memRandomStore {
	return &memRandomStore{reserved: make(map[string]map[string]struct{})}
}

func (m *memRandomStore) Reserve(_ context.Context, scopeKey, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reserved[scopeKey] == nil {
		m.reserved[scopeKey] = make(map[string]struct{})
	}
	if _, exists := m.reserved[scopeKey][value]; exists {
		return refid.ErrRandomCollision
	}
	m.reserved[scopeKey][value] = struct{}{}
	return nil
}

// collidingRandomStore always reports a collision, regardless of scope key or
// value. Used to test exhaustion.
type collidingRandomStore struct{}

func (collidingRandomStore) Reserve(_ context.Context, _, _ string) error {
	return refid.ErrRandomCollision
}

// -----------------------------------------------------------------------
// Config construction helpers
// -----------------------------------------------------------------------

func rtaConfig() refid.Config {
	return refid.Config{
		Lists: map[string][]string{
			"office_location": {"COL", "GAL", "KAN"},
		},
		Issuers: []refid.IssuerConfig{
			{
				Issuer: "RTA",
				Formats: []refid.FormatConfig{
					{
						IDType:   "application_id",
						Timezone: "UTC",
						Segments: []refid.SegmentConfig{
							{Type: refid.SegmentTypeLiteral, Value: "RTA-APP-"},
							{Type: refid.SegmentTypeList, List: "office_location", Param: "officeCode"},
							{Type: refid.SegmentTypeLiteral, Value: "-"},
							{Type: refid.SegmentTypeDate, Layout: "20060102"},
							{Type: refid.SegmentTypeLiteral, Value: "-"},
							{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}:{officeCode}:{yyyyMMdd}", Padding: 6}},
						},
					},
					{
						IDType: "permit_id",
						Segments: []refid.SegmentConfig{
							{Type: refid.SegmentTypeLiteral, Value: "RTA-PMT-"},
							{Type: refid.SegmentTypeList, List: "office_location", Param: "officeCode"},
							{Type: refid.SegmentTypeLiteral, Value: "-"},
							{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}:{officeCode}", Padding: 8}},
						},
					},
				},
			},
		},
	}
}

// -----------------------------------------------------------------------
// Registry construction validation
// -----------------------------------------------------------------------

func TestNewRegistry_ValidConfig(t *testing.T) {
	_, err := refid.NewRegistry(rtaConfig(), refid.WithSequenceStore(newMemStore()))
	if err != nil {
		t.Fatalf("expected no error for valid config, got: %v", err)
	}
}

func TestNewRegistry_DuplicateIDType(t *testing.T) {
	cfg := refid.Config{
		Lists: map[string][]string{"office_location": {"COL"}},
		Issuers: []refid.IssuerConfig{{
			Issuer: "RTA",
			Formats: []refid.FormatConfig{
				{IDType: "application_id", Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeLiteral, Value: "A"},
				}},
				{IDType: "application_id", Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeLiteral, Value: "B"},
				}},
			},
		}},
	}
	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err == nil {
		t.Fatal("expected error for duplicate idType, got nil")
	}
}

func TestNewRegistry_UndefinedList(t *testing.T) {
	cfg := refid.Config{
		Lists: map[string][]string{},
		Issuers: []refid.IssuerConfig{{
			Issuer: "RTA",
			Formats: []refid.FormatConfig{{
				IDType: "application_id",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeList, List: "nonexistent_list", Param: "officeCode"},
				},
			}},
		}},
	}
	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err == nil {
		t.Fatal("expected error for undefined list reference, got nil")
	}
}

func TestNewRegistry_UnknownSegmentType(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "RTA",
			Formats: []refid.FormatConfig{{
				IDType:   "application_id",
				Segments: []refid.SegmentConfig{{Type: "bogus"}},
			}},
		}},
	}
	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err == nil {
		t.Fatal("expected error for unknown segment type, got nil")
	}
}

func TestNewRegistry_EmptySegments(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer:  "RTA",
			Formats: []refid.FormatConfig{{IDType: "application_id", Segments: nil}},
		}},
	}
	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err == nil {
		t.Fatal("expected error for format with no segments, got nil")
	}
}

func TestNewRegistry_MultipleSequenceSegmentsRejected(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType: "bad",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}:a", Padding: 4}},
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}:b", Padding: 4}},
				},
			}},
		}},
	}
	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err == nil {
		t.Fatal("expected error for format with two sequence segments, got nil")
	}
}

func TestNewRegistry_MultipleRandomSegmentsRejected(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType: "bad",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeRandom, Random: &refid.RandomSegmentConfig{ScopeKey: "{issuer}:{idType}:a", Charset: refid.CharsetNumeric, Length: 4}},
					{Type: refid.SegmentTypeRandom, Random: &refid.RandomSegmentConfig{ScopeKey: "{issuer}:{idType}:b", Charset: refid.CharsetNumeric, Length: 4}},
				},
			}},
		}},
	}
	_, err := refid.NewRegistry(cfg, refid.WithRandomStore(newMemRandomStore()))
	if err == nil {
		t.Fatal("expected error for format with two random segments, got nil")
	}
}

func TestNewRegistry_SequenceAndRandomSegmentRejected(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType: "bad",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}", Padding: 4}},
					{Type: refid.SegmentTypeRandom, Random: &refid.RandomSegmentConfig{ScopeKey: "{issuer}:{idType}", Charset: refid.CharsetNumeric, Length: 4}},
				},
			}},
		}},
	}
	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
	if err == nil {
		t.Fatal("expected error for format mixing a sequence and a random segment, got nil")
	}
}

func TestNewRegistry_EmptyIssuerName(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer:  "",
			Formats: []refid.FormatConfig{{IDType: "x", Segments: []refid.SegmentConfig{{Type: refid.SegmentTypeLiteral, Value: "x"}}}},
		}},
	}
	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err == nil {
		t.Fatal("expected error for empty issuer name, got nil")
	}
}

// -----------------------------------------------------------------------
// Generate — happy path
// -----------------------------------------------------------------------

func TestGenerate_LiteralOnly(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType:   "simple",
				Segments: []refid.SegmentConfig{{Type: refid.SegmentTypeLiteral, Value: "HELLO-WORLD"}},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err != nil {
		t.Fatal(err)
	}
	id, err := reg.Generate(context.Background(), "TEST", "simple", nil)
	if err != nil {
		t.Fatal(err)
	}
	if id != "HELLO-WORLD" {
		t.Errorf("expected HELLO-WORLD, got %q", id)
	}
}

func TestGenerate_ApplicationID(t *testing.T) {
	store := newMemStore()
	reg, err := refid.NewRegistry(rtaConfig(), refid.WithSequenceStore(store))
	if err != nil {
		t.Fatal(err)
	}

	// The date portion depends on time.Now(). We call Generate and inspect the
	// structural shape rather than the exact date string.
	id, err := reg.Generate(context.Background(), "RTA", "application_id", map[string]string{"officeCode": "COL"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected shape: "RTA-APP-COL-YYYYMMDD-000001"
	if len(id) != len("RTA-APP-COL-20260817-000001") {
		t.Errorf("unexpected id length %d for id %q", len(id), id)
	}
	if id[:12] != "RTA-APP-COL-" {
		t.Errorf("unexpected prefix: %q", id[:12])
	}
	if id[len(id)-7:] != "-000001" {
		t.Errorf("unexpected suffix: %q", id[len(id)-7:])
	}
}

func TestGenerate_PermitID_NeverResets(t *testing.T) {
	store := newMemStore()
	reg, err := refid.NewRegistry(rtaConfig(), refid.WithSequenceStore(store))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	params := map[string]string{"officeCode": "GAL"}

	for i := 1; i <= 3; i++ {
		id, err := reg.Generate(ctx, "RTA", "permit_id", params)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		expected := "RTA-PMT-GAL-" + string([]byte{
			byte('0' + i/10000000%10),
			byte('0' + i/1000000%10),
			byte('0' + i/100000%10),
			byte('0' + i/10000%10),
			byte('0' + i/1000%10),
			byte('0' + i/100%10),
			byte('0' + i/10%10),
			byte('0' + i%10),
		})
		if id != expected {
			t.Errorf("call %d: expected %q, got %q", i, expected, id)
		}
	}
}

func TestGenerate_SequentialCounters(t *testing.T) {
	store := newMemStore()
	reg, err := refid.NewRegistry(rtaConfig(), refid.WithSequenceStore(store))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	params := map[string]string{"officeCode": "KAN"}

	// Two different issuers/idTypes share the store but must not share counters.
	id1a, _ := reg.Generate(ctx, "RTA", "application_id", params)
	id1b, _ := reg.Generate(ctx, "RTA", "application_id", params)
	id2a, _ := reg.Generate(ctx, "RTA", "permit_id", params)
	id2b, _ := reg.Generate(ctx, "RTA", "permit_id", params)

	appSuffix1 := id1a[len(id1a)-6:]
	appSuffix2 := id1b[len(id1b)-6:]
	if appSuffix1 != "000001" || appSuffix2 != "000002" {
		t.Errorf("application_id counters: got %q then %q", appSuffix1, appSuffix2)
	}

	pmtSuffix1 := id2a[len(id2a)-8:]
	pmtSuffix2 := id2b[len(id2b)-8:]
	if pmtSuffix1 != "00000001" || pmtSuffix2 != "00000002" {
		t.Errorf("permit_id counters: got %q then %q", pmtSuffix1, pmtSuffix2)
	}
}

// -----------------------------------------------------------------------
// Generate — error cases
// -----------------------------------------------------------------------

func TestGenerate_UnknownIssuer(t *testing.T) {
	reg, err := refid.NewRegistry(rtaConfig(), refid.WithSequenceStore(newMemStore()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Generate(context.Background(), "UNKNOWN", "application_id", nil)
	if !errors.Is(err, refid.ErrUnknownIssuer) {
		t.Errorf("expected ErrUnknownIssuer, got %v", err)
	}
}

func TestGenerate_UnknownIDType(t *testing.T) {
	reg, err := refid.NewRegistry(rtaConfig(), refid.WithSequenceStore(newMemStore()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Generate(context.Background(), "RTA", "unknown_id", nil)
	if !errors.Is(err, refid.ErrUnknownIDType) {
		t.Errorf("expected ErrUnknownIDType, got %v", err)
	}
}

func TestGenerate_MissingListParam(t *testing.T) {
	reg, err := refid.NewRegistry(rtaConfig(), refid.WithSequenceStore(newMemStore()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Generate(context.Background(), "RTA", "application_id", map[string]string{})
	if !errors.Is(err, refid.ErrInvalidParam) {
		t.Errorf("expected ErrInvalidParam, got %v", err)
	}
}

func TestGenerate_InvalidListValue(t *testing.T) {
	reg, err := refid.NewRegistry(rtaConfig(), refid.WithSequenceStore(newMemStore()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Generate(context.Background(), "RTA", "application_id", map[string]string{"officeCode": "XYZ"})
	if !errors.Is(err, refid.ErrInvalidParam) {
		t.Errorf("expected ErrInvalidParam, got %v", err)
	}
}

func TestGenerate_ValidationPreventsSideEffects(t *testing.T) {
	store := newMemStore()
	// Config with sequence segment BEFORE list segment
	cfg := refid.Config{
		Lists: map[string][]string{"office_location": {"COL"}},
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType: "seq_before_list",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}", Padding: 4}},
					{Type: refid.SegmentTypeList, List: "office_location", Param: "officeCode"},
				},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(store))
	if err != nil {
		t.Fatal(err)
	}

	// Call with missing officeCode param (list segment will fail validation)
	_, err = reg.Generate(context.Background(), "TEST", "seq_before_list", nil)
	if !errors.Is(err, refid.ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}

	// Assert store was NEVER mutated during the failed call
	if len(store.counters) != 0 {
		t.Errorf("expected 0 counters in store after validation failure, got %d", len(store.counters))
	}
}

func TestGenerate_UnresolvedScopeKeyParam(t *testing.T) {
	// A format with sequence segment using {officeCode} in scopeKey, but NO list segment.
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType: "custom",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeLiteral, Value: "PREFIX-"},
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}:{officeCode}", Padding: 4}},
				},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err != nil {
		t.Fatal(err)
	}

	// Call without officeCode in params -> must fail with ErrInvalidParam
	_, err = reg.Generate(context.Background(), "TEST", "custom", nil)
	if !errors.Is(err, refid.ErrInvalidParam) {
		t.Errorf("expected ErrInvalidParam for unresolved scopeKey placeholder, got %v", err)
	}
}

func TestSegment_DatePlaceholderVariants(t *testing.T) {
	store := newMemStore()
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType:   "date_variants",
				Timezone: "UTC",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}:{yyyy}:{yyyyMM}:{yyyyMMdd}", Padding: 4}},
				},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(store))
	if err != nil {
		t.Fatal(err)
	}

	id, err := reg.Generate(context.Background(), "TEST", "date_variants", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "0001" {
		t.Errorf("expected 0001, got %q", id)
	}
	if len(store.counters) != 1 {
		t.Errorf("expected 1 scope key in store, got %d", len(store.counters))
	}
}

func TestGenerate_CounterOverflow(t *testing.T) {
	// padding:2 → max counter = 99; store returns 100 → overflow
	store := &fixedStore{value: 100}
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType: "seq",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}", Padding: 2}},
				},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(store))
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Generate(context.Background(), "TEST", "seq", nil)
	if !errors.Is(err, refid.ErrCounterOverflow) {
		t.Errorf("expected ErrCounterOverflow, got %v", err)
	}
}

func TestGenerate_CounterDoesNotAdvanceOnOverflow(t *testing.T) {
	store := newMemStore()
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType: "seq",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}", Padding: 1}}, // max counter = 9
				},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(store))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// Increment counter up to max (9)
	for i := 1; i <= 9; i++ {
		_, err := reg.Generate(ctx, "TEST", "seq", nil)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}

	// 10th call should fail with ErrCounterOverflow
	_, err = reg.Generate(ctx, "TEST", "seq", nil)
	if !errors.Is(err, refid.ErrCounterOverflow) {
		t.Fatalf("expected ErrCounterOverflow, got %v", err)
	}

	// Counter in store must stay frozen at 9
	key := "TEST:seq"
	if store.counters[key] != 9 {
		t.Errorf("expected counter in store to remain frozen at 9, got %d", store.counters[key])
	}

	// Subsequent calls must also fail and counter must remain 9
	_, err = reg.Generate(ctx, "TEST", "seq", nil)
	if !errors.Is(err, refid.ErrCounterOverflow) {
		t.Fatalf("expected ErrCounterOverflow on retry, got %v", err)
	}
	if store.counters[key] != 9 {
		t.Errorf("expected counter in store to remain 9 after second overflow attempt, got %d", store.counters[key])
	}
}

// -----------------------------------------------------------------------
// ScopeKey resolution — counter isolation
// -----------------------------------------------------------------------

// clockAt returns a clock that reads *at, so a test moves time by assigning it.
func clockAt(at *time.Time) func() time.Time {
	return func() time.Time { return *at }
}

func utc(year int, month time.Month, day, hour, min, sec int) time.Time {
	return time.Date(year, month, day, hour, min, sec, 0, time.UTC)
}

// A sequence scoped with a date placeholder restarts at 1 when the clock
// crosses that period's boundary and continues within it. A sequence without a
// date placeholder keeps counting across every boundary.
func TestScopeKey_PeriodReset(t *testing.T) {
	type step struct {
		at   time.Time
		want string
	}
	tests := []struct {
		name     string
		scopeKey string
		steps    []step
	}{
		{
			name: "yearly", scopeKey: "{issuer}:{idType}:{yyyy}",
			steps: []step{
				{utc(2026, 12, 31, 23, 59, 58), "0001"},
				{utc(2026, 12, 31, 23, 59, 59), "0002"},
				{utc(2027, 1, 1, 0, 0, 0), "0001"}, // new year
				{utc(2027, 1, 1, 0, 0, 1), "0002"},
				{utc(2027, 6, 15, 12, 0, 0), "0003"}, // same year, months later
			},
		},
		{
			name: "monthly", scopeKey: "{issuer}:{idType}:{yyyyMM}",
			steps: []step{
				{utc(2026, 1, 31, 23, 59, 59), "0001"},
				{utc(2026, 2, 1, 0, 0, 0), "0001"}, // new month
				{utc(2026, 2, 28, 23, 59, 59), "0002"},
				{utc(2027, 2, 1, 0, 0, 0), "0001"}, // same month, a year later
			},
		},
		{
			name: "daily", scopeKey: "{issuer}:{idType}:{yyyyMMdd}",
			steps: []step{
				{utc(2026, 6, 14, 23, 59, 59), "0001"},
				{utc(2026, 6, 15, 0, 0, 0), "0001"}, // new day
				{utc(2026, 6, 15, 12, 0, 0), "0002"},
				{utc(2026, 6, 16, 0, 0, 0), "0001"},
			},
		},
		{
			name: "no date placeholder", scopeKey: "{issuer}:{idType}",
			steps: []step{
				{utc(2026, 12, 31, 23, 59, 59), "0001"},
				{utc(2027, 1, 1, 0, 0, 0), "0002"}, // year boundary
				{utc(2027, 2, 1, 0, 0, 0), "0003"}, // month boundary
				{utc(2027, 2, 2, 0, 0, 0), "0004"}, // day boundary
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := tt.steps[0].at
			reg, err := refid.NewRegistry(refid.Config{Issuers: []refid.IssuerConfig{{
				Issuer: "ACME",
				Formats: []refid.FormatConfig{{
					IDType:   "ref",
					Timezone: "UTC",
					Segments: []refid.SegmentConfig{
						{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: tt.scopeKey, Padding: 4}},
					},
				}},
			}}}, refid.WithSequenceStore(newMemStore()), refid.WithClock(clockAt(&now)))
			if err != nil {
				t.Fatal(err)
			}

			for _, s := range tt.steps {
				now = s.at
				got, err := reg.Generate(context.Background(), "ACME", "ref", nil)
				if err != nil {
					t.Fatalf("at %s: %v", s.at.Format(time.RFC3339), err)
				}
				if got != s.want {
					t.Errorf("at %s: ID = %q, want %q", s.at.Format(time.RFC3339), got, s.want)
				}
			}
		})
	}
}

// A daily scope that also carries a param restarts each param value's count at
// midnight, independently of the others.
func TestScopeKey_DailyResetPerParam(t *testing.T) {
	store := newMemStore()
	now := utc(2026, 6, 14, 10, 0, 0)
	reg, err := refid.NewRegistry(refid.Config{
		Lists: map[string][]string{"office_location": {"COL", "KAN"}},
		Issuers: []refid.IssuerConfig{{
			Issuer: "FCAU",
			Formats: []refid.FormatConfig{{
				IDType:   "case_id",
				Timezone: "UTC",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeList, List: "office_location", Param: "officeCode"},
					{Type: refid.SegmentTypeLiteral, Value: "-"},
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}:{officeCode}:{yyyyMMdd}", Padding: 4}},
				},
			}},
		}},
	}, refid.WithSequenceStore(store), refid.WithClock(clockAt(&now)))
	if err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		at     time.Time
		office string
		want   string
	}{
		{utc(2026, 6, 14, 10, 0, 0), "COL", "COL-0001"},
		{utc(2026, 6, 14, 10, 0, 0), "KAN", "KAN-0001"},
		{utc(2026, 6, 14, 23, 59, 59), "COL", "COL-0002"},
		{utc(2026, 6, 15, 0, 0, 0), "COL", "COL-0001"}, // midnight: COL restarts
		{utc(2026, 6, 15, 0, 0, 1), "COL", "COL-0002"},
		{utc(2026, 6, 15, 0, 0, 2), "KAN", "KAN-0001"}, // KAN restarts on its own first call of the day
	}
	for _, s := range steps {
		now = s.at
		got, err := reg.Generate(context.Background(), "FCAU", "case_id", map[string]string{"officeCode": s.office})
		if err != nil {
			t.Fatalf("at %s: %v", s.at.Format(time.RFC3339), err)
		}
		if got != s.want {
			t.Errorf("at %s: ID = %q, want %q", s.at.Format(time.RFC3339), got, s.want)
		}
	}

	// Two offices over two days: the first day's counters are left as they were.
	if len(store.counters) != 4 {
		t.Errorf("expected 4 scope keys in store, got %d: %v", len(store.counters), store.counters)
	}
	if got := store.counters["FCAU:case_id:COL:20260614"]; got != 2 {
		t.Errorf("COL counter for 14 June = %d, want 2", got)
	}
}

// -----------------------------------------------------------------------
// WithClock
// -----------------------------------------------------------------------

// Generate reads the clock once per ID, so the date segments and the scope key
// of one ID can't come from different instants. The clock here moves a month on
// every read: a second read inside one Generate would show as a month that
// disagrees between the ID's segments, or as a skipped month between IDs.
func TestWithClock_ReadOncePerID(t *testing.T) {
	reads := 0
	base := utc(2026, 11, 15, 12, 0, 0)
	clock := func() time.Time {
		at := base.AddDate(0, reads, 0)
		reads++
		return at
	}
	reg, err := refid.NewRegistry(refid.Config{Issuers: []refid.IssuerConfig{{
		Issuer: "ACME",
		Formats: []refid.FormatConfig{{
			IDType:   "ref",
			Timezone: "UTC",
			Segments: []refid.SegmentConfig{
				{Type: refid.SegmentTypeDate, Layout: "2006"},
				{Type: refid.SegmentTypeLiteral, Value: "/"},
				{Type: refid.SegmentTypeDate, Layout: "01"},
				{Type: refid.SegmentTypeLiteral, Value: "/"},
				{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}:{yyyyMM}", Padding: 4}},
			},
		}},
	}}}, refid.WithSequenceStore(newMemStore()), refid.WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}

	// Each month is new, so each ID is the first of its month's scope.
	for i, want := range []string{"2026/11/0001", "2026/12/0001", "2027/01/0001"} {
		got, err := reg.Generate(context.Background(), "ACME", "ref", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("ID %d = %q, want %q", i+1, got, want)
		}
	}
	if reads != 3 {
		t.Errorf("clock read %d times for 3 IDs, want 3", reads)
	}
}

// The clock supplies an instant. The zone its time carries is ignored: each
// format renders the instant in its own timezone.
func TestWithClock_ClockZoneIsIgnored(t *testing.T) {
	cfg := refid.Config{Issuers: []refid.IssuerConfig{{
		Issuer: "ACME",
		Formats: []refid.FormatConfig{
			{IDType: "colombo_day", Timezone: "Asia/Colombo", Segments: []refid.SegmentConfig{{Type: refid.SegmentTypeDate, Layout: "20060102"}}},
			{IDType: "utc_day", Timezone: "UTC", Segments: []refid.SegmentConfig{{Type: refid.SegmentTypeDate, Layout: "20060102"}}},
		},
	}}}

	// 20:00 UTC on 31 December: already 1 January in Colombo, still 31 December in UTC.
	instant := utc(2026, 12, 31, 20, 0, 0)
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	zones := map[string]*time.Location{
		"UTC":         time.UTC,
		"New York":    newYork,
		"fixed +1400": time.FixedZone("UTC+14", 14*60*60),
	}
	for name, loc := range zones {
		t.Run(name, func(t *testing.T) {
			reg, err := refid.NewRegistry(cfg, refid.WithClock(func() time.Time { return instant.In(loc) }))
			if err != nil {
				t.Fatal(err)
			}
			for idType, want := range map[string]string{"colombo_day": "20270101", "utc_day": "20261231"} {
				got, err := reg.Generate(context.Background(), "ACME", idType, nil)
				if err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("%s = %q, want %q", idType, got, want)
				}
			}
		})
	}
}

func TestWithClock_NilRejected(t *testing.T) {
	_, err := refid.NewRegistry(refid.Config{Issuers: []refid.IssuerConfig{{
		Issuer: "ACME",
		Formats: []refid.FormatConfig{{
			IDType:   "fixed",
			Segments: []refid.SegmentConfig{{Type: refid.SegmentTypeLiteral, Value: "X"}},
		}},
	}}}, refid.WithClock(nil))
	if err == nil {
		t.Fatal("expected an error for a nil clock")
	}
	if !strings.Contains(err.Error(), "WithClock") {
		t.Errorf("error = %q, want it to name WithClock", err)
	}
}

// -----------------------------------------------------------------------
// Concurrency — no duplicate counters from memStore
// -----------------------------------------------------------------------

func TestGenerate_ConcurrentCallsNoDuplicates(t *testing.T) {
	store := newMemStore()
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType: "seq",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}", Padding: 6}},
				},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(store))
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 50
	ctx := context.Background()
	results := make([]string, goroutines)
	var wg sync.WaitGroup
	var failed atomic.Bool

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id, err := reg.Generate(ctx, "TEST", "seq", nil)
			if err != nil {
				t.Errorf("goroutine %d: %v", idx, err)
				failed.Store(true)
				return
			}
			results[idx] = id
		}(i)
	}
	wg.Wait()

	if failed.Load() {
		t.FailNow()
	}

	seen := make(map[string]struct{}, goroutines)
	for _, id := range results {
		if _, dup := seen[id]; dup {
			t.Errorf("duplicate ID generated: %q", id)
		}
		seen[id] = struct{}{}
	}
}

// -----------------------------------------------------------------------
// LoadConfig (smoke test — no real FS needed, just verifying parse logic)
// -----------------------------------------------------------------------

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := refid.LoadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// -----------------------------------------------------------------------
// Segment-level unit tests (via the registry; segments are unexported)
// -----------------------------------------------------------------------

func TestSegment_Literal_EmptyValueRejected(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType:   "bad",
				Segments: []refid.SegmentConfig{{Type: refid.SegmentTypeLiteral, Value: ""}},
			}},
		}},
	}
	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err == nil {
		t.Fatal("expected error for empty literal value, got nil")
	}
}

func TestSegment_Sequence_MissingStoreRejected(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType:   "bad",
				Segments: []refid.SegmentConfig{{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}", Padding: 6}}},
			}},
		}},
	}
	_, err := refid.NewRegistry(cfg)
	if err == nil {
		t.Fatal("expected error for sequence segment without a SequenceStore, got nil")
	}
}

func TestNewRegistry_NoStoresNeededWithoutSequenceOrRandomSegments(t *testing.T) {
	cfg := refid.Config{
		Lists: map[string][]string{"office_location": {"COL"}},
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType:   "simple",
				Timezone: "UTC",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeLiteral, Value: "PREFIX-"},
					{Type: refid.SegmentTypeList, List: "office_location", Param: "officeCode"},
					{Type: refid.SegmentTypeDate, Layout: "20060102"},
				},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("expected no error when no sequence/random segments are used and no stores are supplied, got: %v", err)
	}
	if _, err := reg.Generate(context.Background(), "TEST", "simple", map[string]string{"officeCode": "COL"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSegment_Sequence_EmptyScopeKeyRejected(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType:   "bad",
				Segments: []refid.SegmentConfig{{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "", Padding: 6}}},
			}},
		}},
	}
	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err == nil {
		t.Fatal("expected error for empty sequence scopeKey, got nil")
	}
}

func TestSegment_Sequence_InvalidPaddingRejected(t *testing.T) {
	invalidPaddings := []int{-5, 0, 19, 20, 100}
	for _, pad := range invalidPaddings {
		cfg := refid.Config{
			Issuers: []refid.IssuerConfig{{
				Issuer: "TEST",
				Formats: []refid.FormatConfig{{
					IDType:   "bad",
					Segments: []refid.SegmentConfig{{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}", Padding: pad}}},
				}},
			}},
		}
		_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
		if err == nil {
			t.Errorf("expected error for sequence padding %d, got nil", pad)
		}
	}
}

func TestSegment_Date_EmptyLayoutRejected(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType:   "bad",
				Timezone: "UTC", // so the empty layout, not the missing zone, is what's rejected
				Segments: []refid.SegmentConfig{{Type: refid.SegmentTypeDate, Layout: ""}},
			}},
		}},
	}
	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err == nil {
		t.Fatal("expected error for empty date layout, got nil")
	}
}

// -----------------------------------------------------------------------
// randomSegment
// -----------------------------------------------------------------------

func randomConfig(charset string, length, maxAttempts int) refid.Config {
	return refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType: "voucher",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeLiteral, Value: "V-"},
					{
						Type: refid.SegmentTypeRandom,
						Random: &refid.RandomSegmentConfig{
							ScopeKey:    "{issuer}:{idType}",
							Charset:     charset,
							Length:      length,
							MaxAttempts: maxAttempts,
						},
					},
				},
			}},
		}},
	}
}

func TestGenerate_Random_Numeric(t *testing.T) {
	reg, err := refid.NewRegistry(randomConfig(refid.CharsetNumeric, 6, 0), refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
	if err != nil {
		t.Fatal(err)
	}
	id, err := reg.Generate(context.Background(), "TEST", "voucher", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(id) != len("V-")+6 {
		t.Fatalf("unexpected id length: %q", id)
	}
	suffix := id[len("V-"):]
	for _, c := range suffix {
		if c < '0' || c > '9' {
			t.Errorf("expected numeric charset, got %q in %q", c, id)
		}
	}
}

func TestGenerate_Random_Alpha(t *testing.T) {
	reg, err := refid.NewRegistry(randomConfig(refid.CharsetAlpha, 8, 0), refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
	if err != nil {
		t.Fatal(err)
	}
	id, err := reg.Generate(context.Background(), "TEST", "voucher", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	suffix := id[len("V-"):]
	for _, c := range suffix {
		if c < 'A' || c > 'Z' {
			t.Errorf("expected alpha charset, got %q in %q", c, id)
		}
	}
}

func TestGenerate_Random_NoDuplicatesWithinScope(t *testing.T) {
	// Small charset/length forces frequent collisions, exercising the retry path.
	reg, err := refid.NewRegistry(randomConfig(refid.CharsetNumeric, 2, 50), refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seen := make(map[string]struct{})
	for i := 0; i < 20; i++ {
		id, err := reg.Generate(ctx, "TEST", "voucher", nil)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("call %d: duplicate id %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestGenerate_Random_Exhausted(t *testing.T) {
	reg, err := refid.NewRegistry(randomConfig(refid.CharsetNumeric, 4, 3), refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(collidingRandomStore{}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Generate(context.Background(), "TEST", "voucher", nil)
	if !errors.Is(err, refid.ErrRandomExhausted) {
		t.Errorf("expected ErrRandomExhausted, got %v", err)
	}
}

func TestGenerate_ConcurrentRandomCallsNoDuplicates(t *testing.T) {
	reg, err := refid.NewRegistry(randomConfig(refid.CharsetAlphanumeric, 3, 100), refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 50
	ctx := context.Background()
	results := make([]string, goroutines)
	var wg sync.WaitGroup
	var failed atomic.Bool

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id, err := reg.Generate(ctx, "TEST", "voucher", nil)
			if err != nil {
				t.Errorf("goroutine %d: %v", idx, err)
				failed.Store(true)
				return
			}
			results[idx] = id
		}(i)
	}
	wg.Wait()

	if failed.Load() {
		t.FailNow()
	}

	seen := make(map[string]struct{}, goroutines)
	for _, id := range results {
		if _, dup := seen[id]; dup {
			t.Errorf("duplicate ID generated: %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestSegment_Random_EmptyScopeKeyRejected(t *testing.T) {
	cfg := randomConfig(refid.CharsetNumeric, 6, 0)
	cfg.Issuers[0].Formats[0].Segments[1].Random.ScopeKey = ""
	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
	if err == nil {
		t.Fatal("expected error for empty random scopeKey, got nil")
	}
}

func TestSegment_Random_MissingStoreRejected(t *testing.T) {
	_, err := refid.NewRegistry(randomConfig(refid.CharsetNumeric, 6, 0), refid.WithSequenceStore(newMemStore()))
	if err == nil {
		t.Fatal("expected error for random segment without a RandomStore, got nil")
	}
}

func TestSegment_Random_InvalidCharsetRejected(t *testing.T) {
	_, err := refid.NewRegistry(randomConfig("bogus", 6, 0), refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
	if err == nil {
		t.Fatal("expected error for invalid charset, got nil")
	}
}

func TestSegment_Random_InvalidLengthRejected(t *testing.T) {
	invalidLengths := []int{-1, 0}
	for _, length := range invalidLengths {
		_, err := refid.NewRegistry(randomConfig(refid.CharsetNumeric, length, 0), refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
		if err == nil {
			t.Errorf("expected error for random length %d, got nil", length)
		}
	}
}

func TestSegment_Random_NegativeMaxAttemptsRejected(t *testing.T) {
	_, err := refid.NewRegistry(randomConfig(refid.CharsetNumeric, 6, -1), refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
	if err == nil {
		t.Fatal("expected error for negative maxAttempts, got nil")
	}
}

func TestSegment_Random_MaxAttemptsTooLargeRejected(t *testing.T) {
	_, err := refid.NewRegistry(randomConfig(refid.CharsetNumeric, 6, 101), refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
	if err == nil {
		t.Fatal("expected error for maxAttempts exceeding the cap, got nil")
	}
}

// -----------------------------------------------------------------------
// paramSegment
// -----------------------------------------------------------------------

func paramConfig(pattern string) refid.Config {
	return refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "CDA",
			Formats: []refid.FormatConfig{{
				IDType: "fee_ref",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeParam, Param: "exporterId", Pattern: pattern},
					{Type: refid.SegmentTypeLiteral, Value: "0100002"},
				},
			}},
		}},
	}
}

func TestGenerate_Param_InsertsValueAsIs(t *testing.T) {
	reg, err := refid.NewRegistry(paramConfig(".+"))
	if err != nil {
		t.Fatal(err)
	}
	for _, val := range []string{"0001", "CDA-EXP-000001"} {
		id, err := reg.Generate(context.Background(), "CDA", "fee_ref", map[string]string{"exporterId": val})
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", val, err)
		}
		if want := val + "0100002"; id != want {
			t.Errorf("got %q, want %q", id, want)
		}
	}
}

func TestGenerate_Param_MissingOrEmpty(t *testing.T) {
	reg, err := refid.NewRegistry(paramConfig(".+"))
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []map[string]string{nil, {"exporterId": ""}} {
		_, err := reg.Generate(context.Background(), "CDA", "fee_ref", params)
		if !errors.Is(err, refid.ErrInvalidParam) {
			t.Errorf("params %v: expected ErrInvalidParam, got %v", params, err)
		}
	}
}

func TestGenerate_Param_PatternMatchesWholeValue(t *testing.T) {
	reg, err := refid.NewRegistry(paramConfig("[0-9]{4}"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := reg.Generate(context.Background(), "CDA", "fee_ref", map[string]string{"exporterId": "0001"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "00010100002" {
		t.Errorf("got %q, want %q", id, "00010100002")
	}
	// The pattern is anchored, so a value it only partly matches is rejected.
	// The error names the param, not the value it was given.
	for _, val := range []string{"123", "12345", "00a1", "x1234", "1234\n"} {
		_, err := reg.Generate(context.Background(), "CDA", "fee_ref", map[string]string{"exporterId": val})
		if !errors.Is(err, refid.ErrInvalidParam) {
			t.Errorf("value %q: expected ErrInvalidParam, got %v", val, err)
			continue
		}
		if strings.Contains(err.Error(), val) {
			t.Errorf("value %q: error %q echoes the value", val, err)
		}
	}
}

// A format may hold several param segments, each reading its own key, and may
// read the same key more than once.
func TestGenerate_Param_SeveralParams(t *testing.T) {
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "CDA",
			Formats: []refid.FormatConfig{{
				IDType: "fee_ref",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeParam, Param: "exporterId", Pattern: "[0-9]{4}"},
					{Type: refid.SegmentTypeParam, Param: "mainCategory", Pattern: "[0-9]{2}"},
					{Type: refid.SegmentTypeParam, Param: "subCategory", Pattern: "[0-9]{5}"},
					{Type: refid.SegmentTypeLiteral, Value: "-"},
					{Type: refid.SegmentTypeParam, Param: "mainCategory", Pattern: "[0-9]{2}"},
				},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]string{"exporterId": "0002", "mainCategory": "01", "subCategory": "00002"}
	id, err := reg.Generate(context.Background(), "CDA", "fee_ref", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "00020100002-01"; id != want {
		t.Errorf("got %q, want %q", id, want)
	}

	// Each param is required and checked on its own.
	for _, missing := range []string{"exporterId", "mainCategory", "subCategory"} {
		partial := map[string]string{}
		for k, v := range params {
			if k != missing {
				partial[k] = v
			}
		}
		if _, err := reg.Generate(context.Background(), "CDA", "fee_ref", partial); !errors.Is(err, refid.ErrInvalidParam) {
			t.Errorf("without %s: expected ErrInvalidParam, got %v", missing, err)
		}
	}
	bad := map[string]string{"exporterId": "0002", "mainCategory": "1", "subCategory": "00002"}
	if _, err := reg.Generate(context.Background(), "CDA", "fee_ref", bad); !errors.Is(err, refid.ErrInvalidParam) {
		t.Errorf("mainCategory %q: expected ErrInvalidParam, got %v", bad["mainCategory"], err)
	}
}

func TestGenerate_Param_ValidationPreventsSideEffects(t *testing.T) {
	store := newMemStore()
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "CDA",
			Formats: []refid.FormatConfig{{
				IDType: "cert",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}", Padding: 4}},
					{Type: refid.SegmentTypeParam, Param: "serial", Pattern: "[0-9]{4}/[0-9]{2}"},
				},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(store))
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Generate(context.Background(), "CDA", "cert", map[string]string{"serial": "bad"})
	if !errors.Is(err, refid.ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
	if len(store.counters) != 0 {
		t.Errorf("expected 0 counters in store after validation failure, got %d", len(store.counters))
	}
}

func TestSegment_Param_EmptyParamRejected(t *testing.T) {
	cfg := paramConfig(".+")
	cfg.Issuers[0].Formats[0].Segments[0].Param = ""
	if _, err := refid.NewRegistry(cfg); err == nil {
		t.Fatal("expected error for param segment without a param, got nil")
	}
}

func TestSegment_Param_EmptyPatternRejected(t *testing.T) {
	if _, err := refid.NewRegistry(paramConfig("")); err == nil {
		t.Fatal("expected error for param segment without a pattern, got nil")
	}
}

func TestSegment_Param_InvalidPatternRejected(t *testing.T) {
	if _, err := refid.NewRegistry(paramConfig("([0-9]")); err == nil {
		t.Fatal("expected error for an invalid param pattern, got nil")
	}
}

// -----------------------------------------------------------------------
// example_config.yaml
// -----------------------------------------------------------------------

// The annotated example config compiles, so it stays a working reference.
func TestExampleConfig_Compiles(t *testing.T) {
	cfg, err := refid.LoadConfig("example_config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore())); err != nil {
		t.Fatalf("example_config.yaml does not compile: %v", err)
	}
}

// -----------------------------------------------------------------------
// timezone
//
// Config validation lives here. Tests that pin the clock to see which date a
// zone renders are in registry_internal_test.go.
// -----------------------------------------------------------------------

func tzLiteral() refid.SegmentConfig {
	return refid.SegmentConfig{Type: refid.SegmentTypeLiteral, Value: "X-"}
}

func tzList() refid.SegmentConfig {
	return refid.SegmentConfig{Type: refid.SegmentTypeList, List: "office_location", Param: "officeCode"}
}

func tzParam() refid.SegmentConfig {
	return refid.SegmentConfig{Type: refid.SegmentTypeParam, Param: "payerNo", Pattern: "[0-9]{4}"}
}

func tzDate() refid.SegmentConfig {
	return refid.SegmentConfig{Type: refid.SegmentTypeDate, Layout: "20060102"}
}

func tzSequence(scopeKey string) refid.SegmentConfig {
	return refid.SegmentConfig{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: scopeKey, Padding: 4}}
}

func tzRandom(scopeKey string) refid.SegmentConfig {
	return refid.SegmentConfig{Type: refid.SegmentTypeRandom, Random: &refid.RandomSegmentConfig{ScopeKey: scopeKey, Charset: refid.CharsetNumeric, Length: 4}}
}

// tzConfig is a config with one format, (TZ, tz_format), holding segments and
// the given timezone.
func tzConfig(timezone string, segments ...refid.SegmentConfig) refid.Config {
	return refid.Config{
		Lists: map[string][]string{"office_location": {"COL", "GAL"}},
		Issuers: []refid.IssuerConfig{{
			Issuer:  "TZ",
			Formats: []refid.FormatConfig{{IDType: "tz_format", Timezone: timezone, Segments: segments}},
		}},
	}
}

func newTZRegistry(cfg refid.Config) (refid.Registry, error) {
	return refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
}

// A format that reads the date needs a usable timezone, and a timezone that is
// set must be usable even when no date is read.
func TestNewRegistry_Timezone_Rejected(t *testing.T) {
	tests := []struct {
		name    string
		cfg     refid.Config
		wantMsg string
	}{
		{"date segment, no timezone", tzConfig("", tzDate()), "timezone is required"},
		{"date segment and date scope key, no timezone", tzConfig("", tzDate(), tzSequence("{issuer}:{idType}:{yyyyMMdd}")), "timezone is required"},
		{"sequence {yyyy}, no timezone", tzConfig("", tzSequence("{issuer}:{idType}:{yyyy}")), "timezone is required"},
		{"sequence {yyyyMM}, no timezone", tzConfig("", tzSequence("{issuer}:{idType}:{yyyyMM}")), "timezone is required"},
		{"sequence {yyyyMMdd}, no timezone", tzConfig("", tzSequence("{issuer}:{idType}:{yyyyMMdd}")), "timezone is required"},
		{"random {yyyy}, no timezone", tzConfig("", tzRandom("{issuer}:{idType}:{yyyy}")), "timezone is required"},
		{"random {yyyyMM}, no timezone", tzConfig("", tzRandom("{issuer}:{idType}:{yyyyMM}")), "timezone is required"},
		{"random {yyyyMMdd}, no timezone", tzConfig("", tzRandom("{issuer}:{idType}:{yyyyMMdd}")), "timezone is required"},
		{"date placeholder beside a param placeholder, no timezone", tzConfig("", tzList(), tzSequence("{issuer}:{idType}:{officeCode}:{yyyyMMdd}")), "timezone is required"},
		{"date segment, blank timezone", tzConfig("  ", tzDate()), `timezone "  "`},
		{"unknown zone", tzConfig("Mars/Olympus", tzDate()), "not a known IANA zone name"},
		{"Local", tzConfig("Local", tzDate()), "depends on the host"},
		{"relative path", tzConfig("../etc/localtime", tzDate()), `timezone "../etc/localtime"`},
		{"absolute path", tzConfig("/etc/localtime", tzDate()), `timezone "/etc/localtime"`},
		{"unknown zone on a format with no date", tzConfig("Mars/Olympus", tzLiteral()), "not a known IANA zone name"},
		{"Local on a format with no date", tzConfig("Local", tzLiteral()), "depends on the host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newTZRegistry(tt.cfg)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q does not contain %q", err, tt.wantMsg)
			}
			// The wrapped error says which format to fix.
			if !strings.Contains(err.Error(), `("TZ", "tz_format")`) {
				t.Errorf("error %q does not name the format", err)
			}
		})
	}
}

func TestNewRegistry_Timezone_Accepted(t *testing.T) {
	tests := []struct {
		name string
		cfg  refid.Config
	}{
		// A usable zone, on each way a format can read the date.
		{"UTC", tzConfig("UTC", tzDate())},
		{"Asia/Colombo", tzConfig("Asia/Colombo", tzDate())},
		{"zone with daylight saving", tzConfig("America/New_York", tzDate())},
		{"zone ahead of UTC by 14 hours", tzConfig("Pacific/Kiritimati", tzDate())},
		{"zone with only a date scope key", tzConfig("Asia/Colombo", tzSequence("{issuer}:{idType}:{yyyyMMdd}"))},
		{"zone with a random date scope key", tzConfig("Asia/Colombo", tzRandom("{issuer}:{idType}:{yyyy}"))},

		// No date is read, so no timezone is needed.
		{"literal only", tzConfig("", tzLiteral())},
		{"list and param-scoped sequence", tzConfig("", tzList(), tzSequence("{issuer}:{idType}:{officeCode}"))},
		{"param and literal", tzConfig("", tzParam(), tzLiteral())},
		{"random scoped by issuer and idType", tzConfig("", tzRandom("{issuer}:{idType}"))},
		// Only the exact placeholders count as dates.
		{"look-alike placeholder", tzConfig("", tzSequence("{issuer}:{idType}:{yyyyy}"))},
		{"year text without braces", tzConfig("", tzSequence("{issuer}:{idType}:yyyy"))},

		// A zone nobody reads is harmless.
		{"zone on a format with no date", tzConfig("UTC", tzLiteral())},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newTZRegistry(tt.cfg); err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}

// The zone is a per-idType setting: one format missing it fails even when its
// siblings are fine, and the error points at that format alone.
func TestNewRegistry_Timezone_ErrorNamesOffendingFormat(t *testing.T) {
	cfg := rtaConfig() // application_id reads the date, permit_id does not
	cfg.Issuers[0].Formats[0].Timezone = ""

	_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), `("RTA", "application_id")`) {
		t.Errorf("error %q does not name application_id", err)
	}
	if strings.Contains(err.Error(), "permit_id") {
		t.Errorf("error %q blames permit_id, which reads no date", err)
	}
}

func TestNewRegistry_Timezone_DifferentZonePerFormat(t *testing.T) {
	cfg := refid.Config{Issuers: []refid.IssuerConfig{{
		Issuer: "ACME",
		Formats: []refid.FormatConfig{
			{IDType: "colombo_day", Timezone: "Asia/Colombo", Segments: []refid.SegmentConfig{tzDate()}},
			{IDType: "utc_day", Timezone: "UTC", Segments: []refid.SegmentConfig{tzDate()}},
			{IDType: "fixed", Segments: []refid.SegmentConfig{tzLiteral()}},
		},
	}}}
	if _, err := refid.NewRegistry(cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestLoadConfig_Timezone(t *testing.T) {
	const withZone = `
issuers:
  - issuer: RTA
    formats:
      - idType: application_id
        timezone: Asia/Colombo
        segments:
          - type: date
            layout: "20060102"
          - type: sequence
            sequence:
              scopeKey: "{issuer}:{idType}:{yyyyMMdd}"
              padding: 6
      - idType: permit_id
        segments:
          - type: literal
            value: "RTA-PMT-"
          - type: sequence
            sequence:
              scopeKey: "{issuer}:{idType}"
              padding: 8
`
	load := func(t *testing.T, body string) refid.Config {
		t.Helper()
		path := filepath.Join(t.TempDir(), "refid_config.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := refid.LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}

	t.Run("timezone is read per format", func(t *testing.T) {
		cfg := load(t, withZone)
		formats := cfg.Issuers[0].Formats
		if got := formats[0].Timezone; got != "Asia/Colombo" {
			t.Errorf("application_id timezone = %q, want Asia/Colombo", got)
		}
		if got := formats[1].Timezone; got != "" {
			t.Errorf("permit_id timezone = %q, want empty", got)
		}
		if _, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore())); err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("date format without timezone is rejected", func(t *testing.T) {
		cfg := load(t, strings.Replace(withZone, "        timezone: Asia/Colombo\n", "", 1))
		_, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
		if err == nil || !strings.Contains(err.Error(), "timezone is required") {
			t.Fatalf("expected a timezone is required error, got: %v", err)
		}
	})
}
