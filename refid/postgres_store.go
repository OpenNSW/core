// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"gorm.io/gorm"
)

var validTableIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func validateTableName(name string) error {
	if !validTableIdentifier.MatchString(name) {
		return fmt.Errorf("refid: invalid table name %q: must match [a-zA-Z_][a-zA-Z0-9_]*", name)
	}
	return nil
}

// DefaultTableName is the default PostgreSQL table name used for sequence counters.
const DefaultTableName = "refid_sequences"

// PostgresOption defines a functional option for configuring a PostgreSQL sequence store.
type PostgresOption func(*postgresConfig)

type postgresConfig struct {
	tableName string
	err       error
}

func defaultPostgresConfig() postgresConfig {
	return postgresConfig{
		tableName: DefaultTableName,
	}
}

// WithTableName overrides the default table name ("refid_sequences").
func WithTableName(name string) PostgresOption {
	return func(cfg *postgresConfig) {
		if name != "" {
			if err := validateTableName(name); err != nil {
				cfg.err = err
				return
			}
			cfg.tableName = name
		}
	}
}

// postgresStore is the default SequenceStore backed by PostgreSQL.
// It uses an upsert-and-increment query that is atomic at the database level.
type postgresStore struct {
	db    *gorm.DB
	query string // built once at construction time from the table name
}

// NewPostgresStore returns a SequenceStore backed by PostgreSQL.
// By default it uses the "refid_sequences" table name, which can be overridden
// via WithTableName option. Panics if an invalid table name option is provided.
func NewPostgresStore(db *gorm.DB, opts ...PostgresOption) SequenceStore {
	cfg := defaultPostgresConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.err != nil {
		panic(cfg.err)
	}
	t := cfg.tableName
	query := fmt.Sprintf(`
INSERT INTO %s (scope_key, counter, updated_at)
VALUES (?, 1, now())
ON CONFLICT (scope_key) DO UPDATE
  SET counter    = %s.counter + 1,
      updated_at = now()
WHERE %s.counter < ?
RETURNING counter`, t, t, t)
	return &postgresStore{db: db, query: query}
}

// AutoMigrate creates the sequences table if it does not already exist.
// Options like WithTableName can be passed to migrate a custom table name.
func AutoMigrate(db *gorm.DB, opts ...PostgresOption) error {
	cfg := defaultPostgresConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.err != nil {
		return cfg.err
	}

	sql := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    scope_key  TEXT        NOT NULL PRIMARY KEY,
    counter    BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`, cfg.tableName)

	if err := db.Exec(sql).Error; err != nil {
		return fmt.Errorf("refid: failed to create table %q: %w", cfg.tableName, err)
	}
	return nil
}

// Next atomically increments the counter for scopeKey and returns the new value,
// provided counter < max. If counter >= max, returns ErrCounterOverflow without incrementing.
func (s *postgresStore) Next(ctx context.Context, scopeKey string, max int64) (int64, error) {
	var counter int64
	err := s.db.WithContext(ctx).Raw(s.query, scopeKey, max).Scan(&counter).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("%w: scope %q counter reached max limit %d", ErrCounterOverflow, scopeKey, max)
		}
		return 0, fmt.Errorf("refid: sequence increment failed for scope %q: %w", scopeKey, err)
	}
	if counter == 0 {
		return 0, fmt.Errorf("%w: scope %q counter reached max limit %d", ErrCounterOverflow, scopeKey, max)
	}
	return counter, nil
}
