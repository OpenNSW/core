// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// postgresStore is the default SequenceStore backed by PostgreSQL.
// It uses a single refid_sequences table and an upsert-and-increment query
// that is atomic at the database level — no application-level locks required.
type postgresStore struct {
	db *gorm.DB
}

// NewPostgresStore returns a SequenceStore backed by the provided *gorm.DB.
// Call AutoMigrate (or run the SQL in the package README) to create the
// refid_sequences table before using the store.
func NewPostgresStore(db *gorm.DB) SequenceStore {
	return &postgresStore{db: db}
}

// AutoMigrate creates the refid_sequences table if it does not already exist.
// It is safe to call on every application startup; it is a no-op when the
// table already exists.
func AutoMigrate(db *gorm.DB) error {
	sql := `
CREATE TABLE IF NOT EXISTS refid_sequences (
    scope_key  TEXT        NOT NULL PRIMARY KEY,
    counter    BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`
	if err := db.Exec(sql).Error; err != nil {
		return fmt.Errorf("refid: failed to create refid_sequences table: %w", err)
	}
	return nil
}

// Next atomically increments the counter for scopeKey and returns the new value.
//
// The single upsert query is:
//
//	INSERT INTO refid_sequences (scope_key, counter, updated_at)
//	VALUES ($1, 1, now())
//	ON CONFLICT (scope_key) DO UPDATE
//	  SET counter    = refid_sequences.counter + 1,
//	      updated_at = now()
//	RETURNING counter
//
// PostgreSQL guarantees that this is serialised at the row level, so concurrent
// callers for the same scope key cannot observe duplicate counter values.
func (s *postgresStore) Next(ctx context.Context, scopeKey string) (int64, error) {
	const query = `
INSERT INTO refid_sequences (scope_key, counter, updated_at)
VALUES (?, 1, now())
ON CONFLICT (scope_key) DO UPDATE
  SET counter    = refid_sequences.counter + 1,
      updated_at = now()
RETURNING counter`

	var counter int64
	if err := s.db.WithContext(ctx).Raw(query, scopeKey).Scan(&counter).Error; err != nil {
		return 0, fmt.Errorf("refid: sequence increment failed for scope %q: %w", scopeKey, err)
	}
	return counter, nil
}
