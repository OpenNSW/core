// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/OpenNSW/core/refid"
	"github.com/OpenNSW/core/refid/store/sqlite"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// TestStore_Integration tests Migrate and the SequenceStore against a fresh
// on-disk SQLite database in a temp directory. Unlike the Postgres backend's
// POSTGRES_TEST_DSN-gated integration test, this runs unconditionally — no
// service container or environment gating is needed.
func TestStore_Integration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refid_test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	// 1. Migrate default table
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	// 2. Migrate custom table
	customTable := "refid_integration_test_seqs"
	if err := sqlite.Migrate(ctx, db, sqlite.WithTableName(customTable)); err != nil {
		t.Fatalf("Migrate with custom table failed: %v", err)
	}

	// 3. Create store and test Next increments
	store, err := sqlite.New(db, sqlite.WithTableName(customTable))
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

	// 4. Test overflow handling (max = 2)
	_, err = store.Next(ctx, scope, 2)
	if !errors.Is(err, refid.ErrCounterOverflow) {
		t.Errorf("expected ErrCounterOverflow when counter reaches max, got %v", err)
	}

	// Verify counter in DB remained frozen at 2 after failed overflow call
	var currentCounter int64
	if err := db.QueryRowContext(ctx, "SELECT counter FROM "+customTable+" WHERE scope_key = ?1", scope).Scan(&currentCounter); err != nil {
		t.Fatalf("failed to query counter from db: %v", err)
	}
	if currentCounter != 2 {
		t.Errorf("expected counter in db to remain frozen at 2, got %d", currentCounter)
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
