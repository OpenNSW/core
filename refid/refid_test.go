// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid_test

import (
	"context"
	"errors"
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
						IDType: "application_id",
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
				IDType: "date_variants",
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

func TestScopeKey_DailyReset(t *testing.T) {
	// The scope key includes {yyyyMMdd}, so counters from different days must
	// start fresh. We can observe this with the memStore: generate one ID,
	// check the counter key used, then verify a different date would produce
	// a different scope key (counter would restart at 1).
	//
	// We indirectly validate this by asserting that two calls on the same day
	// produce counters 000001 and 000002, while calls with a different date
	// component would use a separate scope key (hence a different counter bucket).
	store := newMemStore()
	cfg := refid.Config{
		Lists: map[string][]string{"office_location": {"COL"}},
		Issuers: []refid.IssuerConfig{{
			Issuer: "FCAU",
			Formats: []refid.FormatConfig{{
				IDType: "case_id",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeLiteral, Value: "FCAU-"},
					{Type: refid.SegmentTypeList, List: "office_location", Param: "officeCode"},
					{Type: refid.SegmentTypeLiteral, Value: "-"},
					{Type: refid.SegmentTypeDate, Layout: "20060102"},
					{Type: refid.SegmentTypeLiteral, Value: "-"},
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}:{officeCode}:{yyyyMMdd}", Padding: 6}},
				},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(store))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	params := map[string]string{"officeCode": "COL"}

	id1, err := reg.Generate(ctx, "FCAU", "case_id", params)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := reg.Generate(ctx, "FCAU", "case_id", params)
	if err != nil {
		t.Fatal(err)
	}

	suffix1 := id1[len(id1)-6:]
	suffix2 := id2[len(id2)-6:]
	if suffix1 != "000001" {
		t.Errorf("first counter: expected 000001, got %q", suffix1)
	}
	if suffix2 != "000002" {
		t.Errorf("second counter: expected 000002, got %q", suffix2)
	}

	// The total number of scope keys used must be exactly 1 (same day).
	if len(store.counters) != 1 {
		t.Errorf("expected 1 scope key in store, got %d: %v", len(store.counters), store.counters)
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

func TestSegment_Date_UsesUTC(t *testing.T) {
	// Ensure the date segment always uses UTC regardless of local time zone.
	// We can't inject time.Now directly from outside, but we can verify that
	// the generated date component is a valid date string.
	cfg := refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "TEST",
			Formats: []refid.FormatConfig{{
				IDType: "dated",
				Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeDate, Layout: "20060102"},
				},
			}},
		}},
	}
	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(newMemStore()))
	if err != nil {
		t.Fatal(err)
	}
	id, err := reg.Generate(context.Background(), "TEST", "dated", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Must parse as YYYYMMDD
	_, parseErr := time.Parse("20060102", id)
	if parseErr != nil {
		t.Errorf("date segment output %q did not parse as YYYYMMDD: %v", id, parseErr)
	}
}

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
				IDType: "simple",
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
	reg, err := refid.NewRegistry(randomConfig(refid.CharsetAlphanumeric, 3, 200), refid.WithSequenceStore(newMemStore()), refid.WithRandomStore(newMemRandomStore()))
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
