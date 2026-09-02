// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/OpenNSW/core/refid"
	"github.com/OpenNSW/core/refid/sqlite"
)

// openTestDB opens a fresh on-disk SQLite database in a temp directory. No
// service container or environment gating is needed — unlike the Postgres
// backend's POSTGRES_TEST_DSN-gated integration test, these tests run
// unconditionally in-process.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "refid_test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestStore_Increment(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	store, err := sqlite.New(db)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	scope := "RTA:app_id:COL:20260826"

	c1, err := store.Next(ctx, scope, 100)
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if c1 != 1 {
		t.Errorf("expected counter 1, got %d", c1)
	}

	c2, err := store.Next(ctx, scope, 100)
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if c2 != 2 {
		t.Errorf("expected counter 2, got %d", c2)
	}
}

func TestStore_CounterDoesNotAdvanceOnOverflow(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	store, err := sqlite.New(db)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	scope := "OVERFLOW:scope"

	if _, err := store.Next(ctx, scope, 2); err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if _, err := store.Next(ctx, scope, 2); err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	// Counter is now 2 == max; next call must overflow without advancing it.
	_, err = store.Next(ctx, scope, 2)
	if !errors.Is(err, refid.ErrCounterOverflow) {
		t.Errorf("expected ErrCounterOverflow when counter reaches max, got %v", err)
	}

	var counter int64
	if err := db.QueryRowContext(ctx, "SELECT counter FROM refid_sequences WHERE scope_key = ?1", scope).Scan(&counter); err != nil {
		t.Fatalf("failed to query counter from db: %v", err)
	}
	if counter != 2 {
		t.Errorf("expected counter to remain frozen at 2, got %d", counter)
	}
}

func TestNew_InvalidTableName(t *testing.T) {
	invalidNames := []string{
		"users; DROP TABLE users;--",
		"refid table",
		"123refid",
		"refid-sequences",
		"table'quote",
	}

	for _, name := range invalidNames {
		if err := sqlite.Migrate(context.Background(), nil, sqlite.WithTableName(name)); err == nil {
			t.Errorf("expected Migrate error for invalid table name %q, got nil", name)
		}
		if _, err := sqlite.New(nil, sqlite.WithTableName(name)); err == nil {
			t.Errorf("expected New error for invalid table name %q, got nil", name)
		}
	}
}

func TestStore_ConcurrentCallsNoDuplicates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	store, err := sqlite.New(db)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	const goroutines = 50
	scope := "CONCURRENT:scope"
	results := make([]int64, goroutines)
	var wg sync.WaitGroup
	var failed atomic.Bool

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c, err := store.Next(ctx, scope, goroutines)
			if err != nil {
				t.Errorf("goroutine %d: %v", idx, err)
				failed.Store(true)
				return
			}
			results[idx] = c
		}(i)
	}
	wg.Wait()

	if failed.Load() {
		t.FailNow()
	}

	seen := make(map[int64]struct{}, goroutines)
	for _, c := range results {
		if _, dup := seen[c]; dup {
			t.Errorf("duplicate counter value: %d", c)
		}
		seen[c] = struct{}{}
	}
	if len(seen) != goroutines {
		t.Errorf("expected %d distinct counters, got %d", goroutines, len(seen))
	}
}
