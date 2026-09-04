// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package postgres provides a PostgreSQL-backed implementation of
// refid.SequenceStore using database/sql and raw SQL — no ORM. Import a
// PostgreSQL driver (e.g. github.com/jackc/pgx/v5/stdlib), open a connection
// with sql.Open, and pass the resulting *sql.DB to New.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/OpenNSW/core/refid"
	"github.com/OpenNSW/core/refid/store/internal/sqlident"
)

// DefaultTableName is the default table name used for sequence counters.
const DefaultTableName = "refid_sequences"

// Option configures optional behavior for a PostgreSQL SequenceStore.
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

// store is a PostgreSQL-backed implementation of refid.SequenceStore. It
// uses a single atomic upsert-and-increment query, so Next never holds a
// transaction or lock open beyond that one statement.
type store struct {
	db    *sql.DB
	query string // built once at construction time from the table name
}

// New returns a refid.SequenceStore backed by PostgreSQL. db must already be
// opened against the pgx driver (see the package doc). By default it
// targets the "refid_sequences" table; override with WithTableName. New
// returns an error (never panics) if an option is invalid.
func New(db *sql.DB, opts ...Option) (refid.SequenceStore, error) {
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
VALUES ($1, 1, now())
ON CONFLICT (scope_key) DO UPDATE
  SET counter    = %s.counter + 1,
      updated_at = now()
WHERE %s.counter < $2
RETURNING counter`, t, t, t)
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
    scope_key  TEXT        NOT NULL PRIMARY KEY,
    counter    BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`, cfg.tableName)
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("refid/store/postgres: failed to create table %q: %w", cfg.tableName, err)
	}
	return nil
}

// Next implements refid.SequenceStore using a single atomic
// upsert-and-increment statement: no explicit transaction, no lock held
// beyond this one call.
func (s *store) Next(ctx context.Context, scopeKey string, max int64) (int64, error) {
	var counter int64
	err := s.db.QueryRowContext(ctx, s.query, scopeKey, max).Scan(&counter)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: scope %q counter reached max limit %d", refid.ErrCounterOverflow, scopeKey, max)
		}
		return 0, fmt.Errorf("refid/store/postgres: sequence increment failed for scope %q: %w", scopeKey, err)
	}
	return counter, nil
}
