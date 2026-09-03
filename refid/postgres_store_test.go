// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/OpenNSW/core/refid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestPostgresStore_Integration tests AutoMigrate and PostgresStore against a live
// PostgreSQL instance if POSTGRES_TEST_DSN is provided in the environment.
//
// Example DSN: "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
func TestPostgresStore_Integration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("skipping Postgres integration test; set POSTGRES_TEST_DSN to run")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect to postgres: %v", err)
	}

	// 1. AutoMigrate default table
	if err := refid.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	// 2. AutoMigrate custom table
	customTable := "refid_integration_test_seqs"
	if err := refid.AutoMigrate(db, refid.WithTableName(customTable)); err != nil {
		t.Fatalf("AutoMigrate with custom table failed: %v", err)
	}
	defer func() {
		_ = db.Exec("DROP TABLE IF EXISTS " + customTable).Error
	}()

	// 3. Create store and test Next increments
	store := refid.NewPostgresStore(db, refid.WithTableName(customTable))
	ctx := context.Background()
	scope := "RTA:app_id:COL:20260826"

	// 1st increment -> 1
	c1, err := store.Next(ctx, scope, 100)
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if c1 != 1 {
		t.Errorf("expected counter 1, got %d", c1)
	}

	// 2nd increment -> 2
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
	if err := db.Raw("SELECT counter FROM "+customTable+" WHERE scope_key = ?", scope).Scan(&currentCounter).Error; err != nil {
		t.Fatalf("failed to query counter from db: %v", err)
	}
	if currentCounter != 2 {
		t.Errorf("expected counter in db to remain frozen at 2, got %d", currentCounter)
	}
}
