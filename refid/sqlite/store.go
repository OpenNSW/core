// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package sqlite provides a SQLite-backed implementation of refid.Store using
// database/sql and the pure-Go modernc.org/sqlite driver — no CGO, no ORM.
// Open a connection with sql.Open("sqlite", path) (this package blank-imports
// modernc.org/sqlite to register that driver name) and pass the resulting
// *sql.DB to New.
//
// Like refid/postgres, Next is a single atomic upsert-and-increment
// statement: no transaction is held open across calls, so this package makes
// no requirements on the caller's *sql.DB connection-pool configuration.
// SQLite allows only one writer at a time, which simply means concurrent
// Next calls serialize at the database level — a normal throughput
// characteristic, not a correctness concern, and one reason this backend
// suits low-volume counters, local development, and tests well.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/OpenNSW/core/refid"
	"github.com/OpenNSW/core/refid/internal/sqlident"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// DefaultTableName is the default table name used for sequence counters.
const DefaultTableName = "refid_sequences"

// Option configures optional behavior for a SQLite Store.
type Option func(*config)

type config struct {
	tableName string
	err       error
}

func defaultConfig() config { return config{tableName: DefaultTableName} }

// WithTableName overrides the default table name ("refid_sequences"). An
// invalid name (must match [a-zA-Z_][a-zA-Z0-9_]*) is recorded and surfaced
// as an error from New or Migrate — never a panic.
func WithTableName(name string) Option {
	return func(cfg *config) {
		if name == "" {
			return
		}
		if err := sqlident.Validate(name); err != nil {
			cfg.err = err
			return
		}
		cfg.tableName = name
	}
}

// store is a SQLite-backed implementation of refid.Store.
type store struct {
	db    *sql.DB
	query string // built once at construction time from the table name
}

// New returns a refid.Store backed by SQLite. db must already be opened
// against the sqlite driver (see the package doc). By default it targets the
// "refid_sequences" table; override with WithTableName. New returns an error
// (never panics) if an option is invalid.
//
// New pins db's connection pool to a single connection (db.SetMaxOpenConns(1)).
// SQLite allows only one writer at a time regardless; without this, two
// concurrent Next calls can each open their own connection and race into a
// "database is locked" (SQLITE_BUSY) error instead of one simply waiting for
// the other. Since Next is a single, fast statement (no transaction held
// across calls), serializing through one connection costs negligible
// throughput for the low-volume counters this backend is meant for.
func New(db *sql.DB, opts ...Option) (refid.Store, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.err != nil {
		return nil, cfg.err
	}
	db.SetMaxOpenConns(1)
	t := cfg.tableName
	query := fmt.Sprintf(`
INSERT INTO %s (scope_key, counter, updated_at)
VALUES (?1, 1, datetime('now'))
ON CONFLICT (scope_key) DO UPDATE SET
  counter    = counter + 1,
  updated_at = datetime('now')
WHERE counter < ?2
RETURNING counter`, t)
	return &store{db: db, query: query}, nil
}

// Migrate creates the sequence-counter table if it does not already exist.
// Pass WithTableName to migrate a custom table name.
func Migrate(ctx context.Context, db *sql.DB, opts ...Option) error {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.err != nil {
		return cfg.err
	}
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    scope_key  TEXT    NOT NULL PRIMARY KEY,
    counter    INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT    NOT NULL DEFAULT (datetime('now'))
)`, cfg.tableName)
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("refid/sqlite: failed to create table %q: %w", cfg.tableName, err)
	}
	return nil
}

// Next implements refid.Store using a single atomic upsert-and-increment
// statement: no explicit transaction, no lock held beyond this one call.
func (s *store) Next(ctx context.Context, scopeKey string, max int64) (int64, error) {
	var counter int64
	err := s.db.QueryRowContext(ctx, s.query, scopeKey, max).Scan(&counter)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: scope %q counter reached max limit %d", refid.ErrCounterOverflow, scopeKey, max)
		}
		return 0, fmt.Errorf("refid/sqlite: sequence increment failed for scope %q: %w", scopeKey, err)
	}
	return counter, nil
}
