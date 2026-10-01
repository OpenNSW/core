// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package database

import (
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	pg := &PostgresConfig{Host: "localhost", User: "u", Password: "p", Name: "db"}

	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "postgres valid",
			cfg:  Config{Driver: Postgres, Postgres: pg},
		},
		{
			name: "mysql valid",
			cfg:  Config{Driver: MySQL, MySQL: &MySQLConfig{Host: "localhost", User: "u", Password: "p", Name: "db"}},
		},
		{
			name: "sqlite valid",
			cfg:  Config{Driver: SQLite, SQLite: &SQLiteConfig{Path: "app.db"}},
		},
		{
			name: "blocks for other drivers are ignored",
			cfg:  Config{Driver: Postgres, Postgres: pg, SQLite: &SQLiteConfig{}},
		},
		{
			name:    "missing driver",
			cfg:     Config{Postgres: pg},
			wantErr: "database driver is required",
		},
		{
			name:    "unsupported driver",
			cfg:     Config{Driver: "oracle"},
			wantErr: `unsupported database driver "oracle"`,
		},
		{
			name:    "postgres block missing",
			cfg:     Config{Driver: Postgres, SQLite: &SQLiteConfig{Path: "app.db"}},
			wantErr: `postgres config is required when driver is "postgres"`,
		},
		{
			name:    "mysql block missing",
			cfg:     Config{Driver: MySQL},
			wantErr: `mysql config is required when driver is "mysql"`,
		},
		{
			name:    "sqlite block missing",
			cfg:     Config{Driver: SQLite},
			wantErr: `sqlite config is required when driver is "sqlite"`,
		},
		{
			name:    "selected block is validated",
			cfg:     Config{Driver: SQLite, SQLite: &SQLiteConfig{}},
			wantErr: "sqlite path is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertErr(t, tt.cfg.Validate(), tt.wantErr)
		})
	}
}

func TestPostgresConfig_Validate(t *testing.T) {
	base := PostgresConfig{Host: "localhost", User: "user", Password: "secret", Name: "mydb"}

	tests := []struct {
		name    string
		mutate  func(*PostgresConfig)
		wantErr string
	}{
		{name: "valid", mutate: func(*PostgresConfig) {}},
		{name: "missing host", mutate: func(c *PostgresConfig) { c.Host = "" }, wantErr: "postgres host is required"},
		{name: "missing user", mutate: func(c *PostgresConfig) { c.User = "" }, wantErr: "postgres user is required"},
		{name: "missing password", mutate: func(c *PostgresConfig) { c.Password = "" }, wantErr: "postgres password is required"},
		{name: "missing name", mutate: func(c *PostgresConfig) { c.Name = "" }, wantErr: "postgres name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base // copy
			tt.mutate(&cfg)
			assertErr(t, cfg.Validate(), tt.wantErr)
		})
	}
}

func TestMySQLConfig_Validate(t *testing.T) {
	base := MySQLConfig{Host: "localhost", User: "user", Password: "secret", Name: "mydb"}

	tests := []struct {
		name    string
		mutate  func(*MySQLConfig)
		wantErr string
	}{
		{name: "valid", mutate: func(*MySQLConfig) {}},
		{name: "missing host", mutate: func(c *MySQLConfig) { c.Host = "" }, wantErr: "mysql host is required"},
		{name: "missing user", mutate: func(c *MySQLConfig) { c.User = "" }, wantErr: "mysql user is required"},
		{name: "missing password", mutate: func(c *MySQLConfig) { c.Password = "" }, wantErr: "mysql password is required"},
		{name: "missing name", mutate: func(c *MySQLConfig) { c.Name = "" }, wantErr: "mysql name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base // copy
			tt.mutate(&cfg)
			assertErr(t, cfg.Validate(), tt.wantErr)
		})
	}
}

func TestPostgresConfig_DSN(t *testing.T) {
	tests := []struct {
		name string
		cfg  PostgresConfig
		want string
	}{
		{
			name: "with port",
			cfg:  PostgresConfig{Host: "localhost", Port: 5432, User: "user", Password: "secret", Name: "mydb", SSLMode: "disable"},
			want: "postgres://user:secret@localhost:5432/mydb?sslmode=disable",
		},
		{
			name: "without port",
			cfg:  PostgresConfig{Host: "localhost", User: "user", Password: "secret", Name: "mydb", SSLMode: "disable"},
			want: "postgres://user:secret@localhost/mydb?sslmode=disable",
		},
		{
			name: "password with special characters is encoded",
			cfg:  PostgresConfig{Host: "localhost", Port: 5432, User: "user", Password: "p@ss#w0rd!", Name: "mydb", SSLMode: "require"},
			want: "postgres://user:p%40ss%23w0rd%21@localhost:5432/mydb?sslmode=require",
		},
		{
			name: "db name without leading slash gets one added",
			cfg:  PostgresConfig{Host: "db.example.com", User: "admin", Password: "pass", Name: "production", SSLMode: "disable"},
			want: "postgres://admin:pass@db.example.com/production?sslmode=disable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.DSN(); got != tt.want {
				t.Errorf("\ngot:  %s\nwant: %s", got, tt.want)
			}
		})
	}
}

func TestMySQLConfig_DSN(t *testing.T) {
	tests := []struct {
		name string
		cfg  MySQLConfig
		want string
	}{
		{
			name: "with port",
			cfg:  MySQLConfig{Host: "localhost", Port: 3306, User: "user", Password: "secret", Name: "mydb"},
			want: "user:secret@tcp(localhost:3306)/mydb?parseTime=true",
		},
		{
			name: "without port",
			cfg:  MySQLConfig{Host: "localhost", User: "user", Password: "secret", Name: "mydb"},
			want: "user:secret@tcp(localhost)/mydb?parseTime=true",
		},
		{
			name: "ipv6 host with port is bracketed",
			cfg:  MySQLConfig{Host: "::1", Port: 3306, User: "user", Password: "secret", Name: "mydb"},
			want: "user:secret@tcp([::1]:3306)/mydb?parseTime=true",
		},
		{
			name: "password with special characters is passed through",
			cfg:  MySQLConfig{Host: "localhost", User: "user", Password: "p@ss:w0rd!", Name: "mydb"},
			want: "user:p@ss:w0rd!@tcp(localhost)/mydb?parseTime=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.DSN(); got != tt.want {
				t.Errorf("\ngot:  %s\nwant: %s", got, tt.want)
			}
		})
	}
}

func TestSQLiteConfig(t *testing.T) {
	if err := (SQLiteConfig{}).Validate(); err == nil || err.Error() != "sqlite path is required" {
		t.Errorf("empty path: got %v, want %q", err, "sqlite path is required")
	}
	for _, path := range []string{"app.db", "/var/lib/app/app.db", ":memory:"} {
		cfg := SQLiteConfig{Path: path}
		if err := cfg.Validate(); err != nil {
			t.Errorf("%q: unexpected error: %v", path, err)
		}
		if got := cfg.DSN(); got != path {
			t.Errorf("got %q, want %q", got, path)
		}
	}
}

func TestDriver_sqlDriverName(t *testing.T) {
	tests := map[Driver]string{
		Postgres: "pgx",
		MySQL:    "mysql",
		SQLite:   "sqlite",
	}
	for d, want := range tests {
		if got := d.sqlDriverName(); got != want {
			t.Errorf("%s: got %q, want %q", d, got, want)
		}
	}
}

// assertErr fails the test unless err matches wantErr ("" means no error).
func assertErr(t *testing.T, err error, wantErr string) {
	t.Helper()
	if wantErr == "" {
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected error %q, got nil", wantErr)
	}
	if err.Error() != wantErr {
		t.Errorf("got %q, want %q", err.Error(), wantErr)
	}
}
