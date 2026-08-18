// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// DefaultTableName is the default PostgreSQL table name used for sequence counters.
const DefaultTableName = "refid_sequences"

// PostgresOption defines a functional option for configuring a PostgreSQL sequence store.
type PostgresOption func(*postgresConfig)

type postgresConfig struct {
	tableName string
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
// via WithTableName option.
func NewPostgresStore(db *gorm.DB, opts ...PostgresOption) SequenceStore {
	cfg := defaultPostgresConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	t := cfg.tableName
	query := fmt.Sprintf(`
INSERT INTO %s (scope_key, counter, updated_at)
VALUES (?, 1, now())
ON CONFLICT (scope_key) DO UPDATE
  SET counter    = %s.counter + 1,
      updated_at = now()
RETURNING counter`, t, t)
	return &postgresStore{db: db, query: query}
}

// AutoMigrate creates the sequences table if it does not already exist.
// Options like WithTableName can be passed to migrate a custom table name.
func AutoMigrate(db *gorm.DB, opts ...PostgresOption) error {
	cfg := defaultPostgresConfig()
	for _, opt := range opts {
		opt(&cfg)
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

// Next atomically increments the counter for scopeKey and returns the new value.
func (s *postgresStore) Next(ctx context.Context, scopeKey string) (int64, error) {
	var counter int64
	if err := s.db.WithContext(ctx).Raw(s.query, scopeKey).Scan(&counter).Error; err != nil {
		return 0, fmt.Errorf("refid: sequence increment failed for scope %q: %w", scopeKey, err)
	}
	return counter, nil
}
