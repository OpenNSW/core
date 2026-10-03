// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package database

import (
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

func TestPostgresConfig_DSN_ParsesWithPgx(t *testing.T) {
	tests := []struct {
		name     string
		cfg      PostgresConfig
		wantHost string
		wantPort uint16
	}{
		{
			name: "ipv4 host",
			cfg: PostgresConfig{
				Host:     "localhost",
				Port:     5432,
				User:     "test_user",
				Password: "p@ss:w/rd#?",
				Name:     "testdb",
				SSLMode:  "disable",
			},
			wantHost: "localhost",
			wantPort: 5432,
		},
		{
			name: "ipv6 host",
			cfg: PostgresConfig{
				Host:     "::1",
				Port:     5432,
				User:     "test_user",
				Password: "p@ss:w/rd#?",
				Name:     "testdb",
				SSLMode:  "require",
			},
			wantHost: "::1",
			wantPort: 5432,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := pgx.ParseConfig(tt.cfg.DSN())
			if err != nil {
				t.Fatalf("pgx.ParseConfig() error = %v", err)
			}

			if parsed.Host != tt.wantHost {
				t.Errorf("host = %q, want %q", parsed.Host, tt.wantHost)
			}
			if parsed.Port != tt.wantPort {
				t.Errorf("port = %d, want %d", parsed.Port, tt.wantPort)
			}
			if parsed.User != tt.cfg.User {
				t.Errorf("user = %q, want %q", parsed.User, tt.cfg.User)
			}
			if parsed.Password != tt.cfg.Password {
				t.Errorf("password = %q, want %q", parsed.Password, tt.cfg.Password)
			}
			if parsed.Database != tt.cfg.Name {
				t.Errorf("database = %q, want %q", parsed.Database, tt.cfg.Name)
			}
			if parsed.RuntimeParams["sslmode"] != tt.cfg.SSLMode {
				t.Errorf("sslmode = %q, want %q", parsed.RuntimeParams["sslmode"], tt.cfg.SSLMode)
			}
		})
	}
}

func TestMySQLConfig_DSN_ParsesWithDriver(t *testing.T) {
	tests := []struct {
		name     string
		cfg      MySQLConfig
		wantAddr string
	}{
		{
			name: "ipv4 host",
			cfg: MySQLConfig{
				Host:     "localhost",
				Port:     3306,
				User:     "test_user",
				Password: "p@ss:w/rd#?",
				Name:     "testdb",
			},
			wantAddr: "localhost:3306",
		},
		{
			name: "ipv6 host",
			cfg: MySQLConfig{
				Host:     "::1",
				Port:     3306,
				User:     "test_user",
				Password: "p@ss:w/rd#?",
				Name:     "testdb",
			},
			wantAddr: "[::1]:3306",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := mysql.ParseDSN(tt.cfg.DSN())
			if err != nil {
				t.Fatalf("mysql.ParseDSN() error = %v", err)
			}

			if parsed.Net != "tcp" {
				t.Errorf("network = %q, want %q", parsed.Net, "tcp")
			}
			if parsed.Addr != tt.wantAddr {
				t.Errorf("address = %q, want %q", parsed.Addr, tt.wantAddr)
			}
			if parsed.User != tt.cfg.User {
				t.Errorf("user = %q, want %q", parsed.User, tt.cfg.User)
			}
			if parsed.Passwd != tt.cfg.Password {
				t.Errorf("password = %q, want %q", parsed.Passwd, tt.cfg.Password)
			}
			if parsed.DBName != tt.cfg.Name {
				t.Errorf("database = %q, want %q", parsed.DBName, tt.cfg.Name)
			}
			if !parsed.ParseTime {
				t.Error("parseTime = false, want true")
			}
		})
	}
}
