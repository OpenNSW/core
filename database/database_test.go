// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package database

import (
	"context"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestHealthCheck_NilDB verifies HealthCheck returns a meaningful error for nil.
func TestHealthCheck_NilDB(t *testing.T) {
	err := HealthCheck(context.Background(), nil)
	if err == nil {
		t.Fatal("expected an error for nil db, got nil")
	}
	const want = "database is nil"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

// TestNew_InvalidConfig verifies New rejects an invalid config without ever
// attempting a real connection.
func TestNew_InvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name:    "missing driver",
			cfg:     Config{SQLite: &SQLiteConfig{Path: ":memory:"}},
			wantErr: "database driver is required",
		},
		{
			name:    "missing block for driver",
			cfg:     Config{Driver: Postgres},
			wantErr: `postgres config is required when driver is "postgres"`,
		},
		{
			name:    "invalid block",
			cfg:     Config{Driver: Postgres, Postgres: &PostgresConfig{User: "u", Password: "p", Name: "db"}},
			wantErr: "postgres host is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(context.Background(), tt.cfg)
			assertErr(t, err, tt.wantErr)
		})
	}
}

// TestNew_SQLiteInMemory exercises the full open/pool/ping path and HealthCheck
// against a real in-memory SQLite database.
func TestNew_SQLiteInMemory(t *testing.T) {
	cfg := Config{
		Driver: SQLite,
		SQLite: &SQLiteConfig{
			Path: ":memory:",
			Pool: PoolConfig{MaxOpenConns: 1, MaxConnLifetimeSeconds: 60},
		},
	}

	db, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New returned unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections: got %d, want 1", got)
	}

	if err := HealthCheck(context.Background(), db); err != nil {
		t.Errorf("HealthCheck returned unexpected error: %v", err)
	}

	var one int
	if err := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if one != 1 {
		t.Errorf("got %d, want 1", one)
	}
}

// TestNew_UsesSelectedPool verifies only the selected driver's pool settings
// are applied.
func TestNew_UsesSelectedPool(t *testing.T) {
	cfg := Config{
		Driver:   SQLite,
		SQLite:   &SQLiteConfig{Path: ":memory:", Pool: PoolConfig{MaxOpenConns: 1}},
		Postgres: &PostgresConfig{Pool: PoolConfig{MaxOpenConns: 25}},
	}

	db, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New returned unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections: got %d, want 1", got)
	}
}

// TestHealthCheck_ClosedDB verifies HealthCheck reports a closed pool as unhealthy.
func TestHealthCheck_ClosedDB(t *testing.T) {
	db, err := New(context.Background(), Config{Driver: SQLite, SQLite: &SQLiteConfig{Path: ":memory:"}})
	if err != nil {
		t.Fatalf("New returned unexpected error: %v", err)
	}
	_ = db.Close()

	if err := HealthCheck(context.Background(), db); err == nil {
		t.Error("expected an error for a closed db, got nil")
	}
}

// TestNew_DriverNotImported verifies a missing driver import surfaces as an
// error rather than a panic. The MySQL driver is not imported by this test
// binary.
func TestNew_DriverNotImported(t *testing.T) {
	cfg := Config{
		Driver: MySQL,
		MySQL:  &MySQLConfig{Host: "127.0.0.1", User: "user", Password: "password", Name: "testdb"},
	}

	_, err := New(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected an error for an unregistered driver, got nil")
	}
	if !strings.Contains(err.Error(), `unknown driver "mysql"`) {
		t.Errorf("got %q, want it to mention the unknown driver", err.Error())
	}
}

func TestPoolConfig_apply(t *testing.T) {
	db, err := New(context.Background(), Config{Driver: SQLite, SQLite: &SQLiteConfig{Path: ":memory:"}})
	if err != nil {
		t.Fatalf("New returned unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Zero values leave database/sql defaults (unlimited open connections).
	PoolConfig{}.apply(db)
	if got := db.Stats().MaxOpenConnections; got != 0 {
		t.Errorf("zero PoolConfig: MaxOpenConnections got %d, want 0", got)
	}

	PoolConfig{MaxOpenConns: 7, MaxIdleConns: 3, MaxConnLifetimeSeconds: int(time.Minute / time.Second)}.apply(db)
	if got := db.Stats().MaxOpenConnections; got != 7 {
		t.Errorf("MaxOpenConnections: got %d, want 7", got)
	}
}
