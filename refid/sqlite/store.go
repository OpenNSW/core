// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package sqlite provides a SQLite-backed implementation of refid.Store using
// database/sql and the pure-Go modernc.org/sqlite driver — no CGO, no ORM.
// Open a connection with sql.Open("sqlite", path) (this package blank-imports
// modernc.org/sqlite to register that driver name) and pass the resulting
// *sql.DB to New.
//
// Like refid/postgres, Next is a single atomic upsert-and-increment
// statement: no transaction is held open across calls, and this package
// never modifies the caller's *sql.DB connection-pool configuration — it may
// be shared with the rest of the caller's application.
//
// # Concurrency
//
// SQLite allows only one writer at a time, enforced with a whole-database
// file lock (not per-row, unlike Postgres). The returned Store guards Next
// with an in-process mutex, so concurrent Next calls from goroutines sharing
// one Store never race each other into a "database is locked" (SQLITE_BUSY)
// error — they simply queue.
//
// That mutex only covers this one Store instance within this one process. It
// does not protect against a second connection on the same *sql.DB (e.g. a
// transaction held open elsewhere in the caller's application), a second
// *sql.DB opened against the same file, or a second OS process — any of
// those can still hit SQLITE_BUSY immediately, because nothing sets SQLite's
// busy_timeout by default. If you need Next to wait instead of failing in
// those cases, open the database with a busy timeout in the DSN, e.g.
// sql.Open("sqlite", "file:refid.db?_busy_timeout=5000") (or the equivalent
// _pragma=busy_timeout(5000) form) — a value New cannot set on your behalf,
// since it only ever receives an already-opened *sql.DB.
//
// In short: this backend satisfies refid.Store's concurrency contract for a
// single process out of the box. For a deployment with multiple process
// instances sharing one SQLite file, either configure busy_timeout as above
// or prefer refid/postgres, which has no equivalent limitation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

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

	// mu serializes Next calls on this Store instance so concurrent
	// goroutines within this process queue instead of racing each other
	// into SQLITE_BUSY. See the package doc's "Concurrency" section for
	// what this does and does not protect against.
	mu sync.Mutex
}

// New returns a refid.Store backed by SQLite. db must already be opened
// against the sqlite driver (see the package doc). By default it targets the
// "refid_sequences" table; override with WithTableName. New returns an error
// (never panics) if an option is invalid.
//
// New never modifies db's connection-pool configuration (no SetMaxOpenConns
// or similar) — db may be shared with the rest of your application. See the
// package doc's "Concurrency" section for how Next stays safe under
// concurrent use, and its limits.
func New(db *sql.DB, opts ...Option) (refid.Store, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.err != nil {
		return nil, cfg.err
	}
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
// Concurrent calls on this Store instance are serialized by an in-process
// mutex — see the package doc's "Concurrency" section.
func (s *store) Next(ctx context.Context, scopeKey string, max int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
