// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package database

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	integrationMaxOpen         = 4
	integrationMaxIdle         = 1
	integrationLifetimeSeconds = 60
)

func TestPostgresIntegration(t *testing.T) {
	host := os.Getenv("DATABASE_TEST_POSTGRES_HOST")
	if host == "" {
		t.Skip("DATABASE_TEST_POSTGRES_HOST is not set")
	}

	cfg := Config{
		Driver: Postgres,
		Postgres: &PostgresConfig{
			Host:     host,
			Port:     integrationPort(t, "DATABASE_TEST_POSTGRES_PORT", 5432),
			User:     integrationRequired(t, "DATABASE_TEST_POSTGRES_USER"),
			Password: integrationRequired(t, "DATABASE_TEST_POSTGRES_PASSWORD"),
			Name:     integrationRequired(t, "DATABASE_TEST_POSTGRES_NAME"),
			SSLMode:  "disable",
			Pool: PoolConfig{
				MaxOpenConns:           integrationMaxOpen,
				MaxIdleConns:           integrationMaxIdle,
				MaxConnLifetimeSeconds: integrationLifetimeSeconds,
			},
		},
	}

	testLiveDatabase(t, cfg)
}

func TestMySQLIntegration(t *testing.T) {
	host := os.Getenv("DATABASE_TEST_MYSQL_HOST")
	if host == "" {
		t.Skip("DATABASE_TEST_MYSQL_HOST is not set")
	}

	cfg := Config{
		Driver: MySQL,
		MySQL: &MySQLConfig{
			Host:     host,
			Port:     integrationPort(t, "DATABASE_TEST_MYSQL_PORT", 3306),
			User:     integrationRequired(t, "DATABASE_TEST_MYSQL_USER"),
			Password: integrationRequired(t, "DATABASE_TEST_MYSQL_PASSWORD"),
			Name:     integrationRequired(t, "DATABASE_TEST_MYSQL_NAME"),
			Pool: PoolConfig{
				MaxOpenConns:           integrationMaxOpen,
				MaxIdleConns:           integrationMaxIdle,
				MaxConnLifetimeSeconds: integrationLifetimeSeconds,
			},
		},
	}

	testLiveDatabase(t, cfg)
}

func testLiveDatabase(t *testing.T, cfg Config) {
	t.Helper()

	ctx := context.Background()
	db, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New returned unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stats := db.Stats()
	if stats.MaxOpenConnections != integrationMaxOpen {
		t.Errorf("MaxOpenConnections = %d, want %d", stats.MaxOpenConnections, integrationMaxOpen)
	}

	conns := make([]*sql.Conn, 0, integrationMaxIdle+1)
	for i := 0; i < integrationMaxIdle+1; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("db.Conn() failed for connection %d: %v", i+1, err)
		}
		if err := conn.PingContext(ctx); err != nil {
			_ = conn.Close()
			t.Fatalf("connection %d ping failed: %v", i+1, err)
		}
		conns = append(conns, conn)
	}
	for _, conn := range conns {
		if err := conn.Close(); err != nil {
			t.Errorf("closing pooled connection failed: %v", err)
		}
	}

	stats = db.Stats()
	if stats.Idle > integrationMaxIdle {
		t.Errorf("Idle connections = %d, want at most %d", stats.Idle, integrationMaxIdle)
	}

	if err := HealthCheck(ctx, db); err != nil {
		t.Fatalf("HealthCheck returned unexpected error: %v", err)
	}

	var one int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1 failed: %v", err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 returned %d, want 1", one)
	}
}

func integrationRequired(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		t.Fatalf("%s must be set when the integration host is configured", key)
	}
	return value
}

func integrationPort(t *testing.T, key string, defaultPort int) int {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		return defaultPort
	}

	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port, got %q", key, value)
	}
	return port
}
