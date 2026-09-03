// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/OpenNSW/core/refid"
	"github.com/OpenNSW/core/refid/store/postgres"
)

// TestStore_Integration tests Migrate and the SequenceStore against a live
// PostgreSQL instance if POSTGRES_TEST_DSN is provided in the environment.
//
// Example DSN: "host=localhost port=5432 user=postgres password=postgres dbname=refid_test sslmode=disable"
func TestStore_Integration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("skipping Postgres integration test; set POSTGRES_TEST_DSN to run")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("failed to open postgres connection: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("failed to connect to postgres: %v", err)
	}

	// 1. Migrate default table
	if err := postgres.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	// 2. Migrate custom table
	customTable := "refid_integration_test_seqs"
	if err := postgres.Migrate(ctx, db, postgres.WithTableName(customTable)); err != nil {
		t.Fatalf("Migrate with custom table failed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DROP TABLE IF EXISTS " + customTable)
	})

	// 3. Create store and test Next increments
	store, err := postgres.New(db, postgres.WithTableName(customTable))
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
	if err := db.QueryRowContext(ctx, "SELECT counter FROM "+customTable+" WHERE scope_key = $1", scope).Scan(&currentCounter); err != nil {
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
		if err := postgres.Migrate(context.Background(), nil, postgres.WithTableName(name)); err == nil {
			t.Errorf("expected Migrate error for invalid table name %q, got nil", name)
		}
		if _, err := postgres.New(nil, postgres.WithTableName(name)); err == nil {
			t.Errorf("expected New error for invalid table name %q, got nil", name)
		}
	}
}
