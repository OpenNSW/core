// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/OpenNSW/core/database"
)

// writeConfig writes yamlContent to a temp file and points CONFIG_PATH at
// it, so LoadConfig() reads exactly this fixture.
func writeConfig(t *testing.T, yamlContent string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o600); err != nil {
		t.Fatalf("writing config fixture: %v", err)
	}
	t.Setenv("CONFIG_PATH", path)
}

func TestLoadConfig_Defaults(t *testing.T) {
	writeConfig(t, "db:\n  driver: sqlite\n")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.DB.SQLite.Path != "./agency_applications.db" {
		t.Errorf("DB.Path = %q, want ./agency_applications.db", cfg.DB.SQLite.Path)
	}
	if cfg.Dir != "./migrations" {
		t.Errorf("Dir = %q, want ./migrations", cfg.Dir)
	}
}

func TestLoadConfig_SQLite(t *testing.T) {
	writeConfig(t, `db:
  driver: sqlite
  sqlite:
    path: ./custom.db
migrationDir: ./db/migrations
`)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.DB.Driver != "sqlite" {
		t.Errorf("DB.Driver = %q, want sqlite", cfg.DB.Driver)
	}
	if cfg.DB.SQLite.Path != "./custom.db" {
		t.Errorf("DB.Path = %q, want ./custom.db", cfg.DB.SQLite.Path)
	}
	if cfg.Dir != "./db/migrations" {
		t.Errorf("Dir = %q, want ./db/migrations", cfg.Dir)
	}
}

func TestLoadConfig_Postgres(t *testing.T) {
	writeConfig(t, `db:
  driver: postgres
  postgres:
    host: db.example.com
    port: 5433
    user: admin
    password: secret
    name: mydb
    sslMode: require
`)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.DB.Driver != "postgres" {
		t.Errorf("DB.Driver = %q, want postgres", cfg.DB.Driver)
	}
	if cfg.DB.Postgres.Host != "db.example.com" {
		t.Errorf("DB.Host = %q, want db.example.com", cfg.DB.Postgres.Host)
	}
	if cfg.DB.Postgres.Port != 5433 {
		t.Errorf("DB.Port = %d, want 5433", cfg.DB.Postgres.Port)
	}
	if cfg.DB.Postgres.User != "admin" {
		t.Errorf("DB.User = %q, want admin", cfg.DB.Postgres.User)
	}
	if cfg.DB.Postgres.Password != "secret" {
		t.Errorf("DB.Password = %q, want secret", cfg.DB.Postgres.Password)
	}
	if cfg.DB.Postgres.Name != "mydb" {
		t.Errorf("DB.Name = %q, want mydb", cfg.DB.Postgres.Name)
	}
	if cfg.DB.Postgres.SSLMode != "require" {
		t.Errorf("DB.SSLMode = %q, want require", cfg.DB.Postgres.SSLMode)
	}
}

// The db.postgres.password field can be a "{{env:NAME}}" placeholder.
func TestLoadConfig_Postgres_ResolvesPasswordFromEnv(t *testing.T) {
	t.Setenv("MIGRATE_TEST_DB_PASSWORD", "s3cr3t")
	writeConfig(t, `db:
  driver: postgres
  postgres:
    host: localhost
    user: admin
    password: "{{env:MIGRATE_TEST_DB_PASSWORD}}"
    name: mydb
`)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.DB.Postgres.Password != "s3cr3t" {
		t.Errorf("DB.Password = %q, want s3cr3t", cfg.DB.Postgres.Password)
	}
}

// A "{{env:NAME}}" placeholder for db.postgres.port decodes into the int port.
func TestLoadConfig_Postgres_ResolvesPortFromEnv(t *testing.T) {
	t.Setenv("MIGRATE_TEST_DB_PORT", "5434")
	writeConfig(t, `db:
  driver: postgres
  postgres:
    host: localhost
    port: "{{env:MIGRATE_TEST_DB_PORT}}"
    user: admin
    password: secret
    name: mydb
`)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.DB.Postgres.Port != 5434 {
		t.Errorf("DB.Port = %d, want 5434", cfg.DB.Postgres.Port)
	}
}

func TestLoadConfig_Postgres_MissingBlock(t *testing.T) {
	writeConfig(t, "db:\n  driver: postgres\n")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error when the postgres block is missing, got nil")
	}
}

func TestLoadConfig_Postgres_RequiredFields(t *testing.T) {
	fields := map[string]string{
		"host":     "localhost",
		"user":     "admin",
		"password": "secret",
		"name":     "mydb",
	}
	for missing := range fields {
		t.Run(missing, func(t *testing.T) {
			yaml := "db:\n  driver: postgres\n  postgres:\n"
			for field, value := range fields {
				if field != missing {
					yaml += "    " + field + ": " + value + "\n"
				}
			}
			writeConfig(t, yaml)

			_, err := LoadConfig()
			if err == nil {
				t.Fatalf("expected error when %s is missing, got nil", missing)
			}
		})
	}
}

func TestLoadConfig_Postgres_DefaultSSLModeRequire(t *testing.T) {
	writeConfig(t, `db:
  driver: postgres
  postgres:
    host: localhost
    user: admin
    password: secret
    name: mydb
`)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.DB.Postgres.SSLMode != "require" {
		t.Errorf("DB.Postgres.SSLMode = %q, want require when unset", cfg.DB.Postgres.SSLMode)
	}
}

func TestLoadConfig_Postgres_KeepsSSLMode(t *testing.T) {
	writeConfig(t, `db:
  driver: postgres
  postgres:
    host: localhost
    user: admin
    password: secret
    name: mydb
    sslMode: disable
`)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.DB.Postgres.SSLMode != "disable" {
		t.Errorf("DB.Postgres.SSLMode = %q, want disable", cfg.DB.Postgres.SSLMode)
	}
}

// An empty config file, such as /dev/null, gives the defaults. generate
// needs no database, so it can run without a config of its own.
func TestLoadConfig_EmptyFile(t *testing.T) {
	for _, path := range []string{os.DevNull, filepath.Join(t.TempDir(), "empty.yaml")} {
		t.Run(path, func(t *testing.T) {
			if path != os.DevNull {
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatalf("writing config fixture: %v", err)
				}
			}
			t.Setenv("CONFIG_PATH", path)

			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if cfg.DB.Driver != database.SQLite {
				t.Errorf("DB.Driver = %q, want sqlite", cfg.DB.Driver)
			}
			if cfg.Dir != "./migrations" {
				t.Errorf("Dir = %q, want ./migrations", cfg.Dir)
			}
		})
	}
}

func TestLoadConfig_UnsupportedDriver(t *testing.T) {
	writeConfig(t, "db:\n  driver: mysql\n")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for unsupported driver, got nil")
	}
}

// The migrator supports sqlite and postgres only, so a complete mysql block,
// which database.Config accepts, is still refused.
func TestLoadConfig_MySQLRefused(t *testing.T) {
	writeConfig(t, `db:
  driver: mysql
  mysql:
    host: localhost
    user: admin
    password: secret
    name: mydb
`)

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for the mysql driver, got nil")
	}
}

// The command registers the database/sql drivers database.New opens.
func TestDriversRegistered(t *testing.T) {
	for _, name := range []string{"pgx", "sqlite"} {
		if !slices.Contains(sql.Drivers(), name) {
			t.Errorf("database/sql driver %q is not registered", name)
		}
	}
}

func TestOpenSQLite(t *testing.T) {
	writeConfig(t, "db:\n  driver: sqlite\n  sqlite:\n    path: "+filepath.Join(t.TempDir(), "test.db")+"\n")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	db, err := database.New(context.Background(), cfg.DB)
	if err != nil {
		t.Fatalf("database.New() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestLoadConfig_MissingConfigFile(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "does-not-exist.yaml"))

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected an error for a missing config file")
	}
}
