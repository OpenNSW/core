// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
)

// New opens a connection pool for the configured driver, applies its pool
// settings, and pings the server.
//
// The package does not import any driver; the caller must blank-import the one
// matching cfg.Driver (see the Driver constants).
func New(ctx context.Context, cfg Config) (*sql.DB, error) {
	conn, err := cfg.selected()
	if err != nil {
		return nil, err
	}
	if err := conn.Validate(); err != nil {
		return nil, err
	}

	db, err := sql.Open(cfg.Driver.sqlDriverName(), conn.DSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	conn.poolConfig().apply(db)

	// Test the connection
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close() // Attempt to close the connection if ping fails
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("database connection established",
		append([]any{"driver", cfg.Driver}, conn.logArgs()...)...,
	)

	return db, nil
}

// HealthCheck performs a health check on the database connection.
func HealthCheck(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	return nil
}
