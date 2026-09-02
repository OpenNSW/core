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
	"time"

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

// TestStore_NextDoesNotHangBehindHeldTransaction proves Next fails promptly
// rather than hanging forever when it can't get SQLite's write lock, even
// though Store's own mutex (see the package doc's "Concurrency" section)
// only serializes calls made through this one Store — it can't see or wait
// on a transaction opened directly against the same *sql.DB elsewhere in the
// caller's application. Before New stopped pinning the pool to a single
// connection, this scenario deadlocked: Next would block forever waiting for
// a connection the held transaction was never going to release.
//
// Next is deliberately called with context.Background() (no deadline) here:
// database/sql's own connection-pool checkout also respects a ctx deadline
// while waiting for a free connection, so a short-deadline ctx passed
// straight to Next can't tell "the pool is exhausted and will never free up"
// apart from "Next got its own connection and simply failed fast on
// SQLite's file lock" — both would return within the same short window. Only
// an unbounded ctx, guarded by an external test-level timeout (via the
// goroutine+select below, not anything Next itself respects), can actually
// distinguish a genuine hang from a bounded failure.
func TestStore_NextDoesNotHangBehindHeldTransaction(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	store, err := sqlite.New(db)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Simulate another part of the caller's application holding a write
	// transaction open on the same *sql.DB — this takes SQLite's
	// whole-database write lock and does not release it until Commit or
	// Rollback.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.ExecContext(ctx, "INSERT INTO refid_sequences (scope_key, counter, updated_at) VALUES ('holder', 0, datetime('now'))"); err != nil {
		t.Fatalf("failed to take the write lock via tx: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := store.Next(context.Background(), "OTHER:scope", 100)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected Next to fail while the write lock is held by another transaction, got nil error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Next did not return within 2s — it appears to be waiting forever for a pooled " +
			"connection that a transaction held elsewhere will never release")
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
