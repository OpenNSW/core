// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package database

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

// Driver identifies the database engine to connect to.
type Driver string

const (
	// Postgres connects through the pgx driver (github.com/jackc/pgx/v5/stdlib).
	Postgres Driver = "postgres"
	// MySQL connects through github.com/go-sql-driver/mysql.
	MySQL Driver = "mysql"
	// SQLite connects through modernc.org/sqlite.
	SQLite Driver = "sqlite"
)

// sqlDriverName returns the name the driver registers with database/sql.
func (d Driver) sqlDriverName() string {
	switch d {
	case Postgres:
		return "pgx"
	case MySQL:
		return "mysql"
	case SQLite:
		return "sqlite"
	default:
		return string(d)
	}
}

// Config selects a driver and holds the configuration for each supported
// database. Only the block matching Driver is used; the others are ignored.
type Config struct {
	Driver   Driver          `yaml:"driver"`
	Postgres *PostgresConfig `yaml:"postgres"`
	MySQL    *MySQLConfig    `yaml:"mysql"`
	SQLite   *SQLiteConfig   `yaml:"sqlite"`
}

// connConfig is implemented by each driver-specific config.
type connConfig interface {
	Validate() error
	DSN() string
	poolConfig() PoolConfig
	logArgs() []any
}

// selected returns the config block for the configured driver.
func (c Config) selected() (connConfig, error) {
	switch c.Driver {
	case "":
		return nil, fmt.Errorf("database driver is required")
	case Postgres:
		if c.Postgres == nil {
			return nil, fmt.Errorf("postgres config is required when driver is %q", Postgres)
		}
		return c.Postgres, nil
	case MySQL:
		if c.MySQL == nil {
			return nil, fmt.Errorf("mysql config is required when driver is %q", MySQL)
		}
		return c.MySQL, nil
	case SQLite:
		if c.SQLite == nil {
			return nil, fmt.Errorf("sqlite config is required when driver is %q", SQLite)
		}
		return c.SQLite, nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", c.Driver)
	}
}

// Validate checks that the block for the configured driver is present and valid.
func (c Config) Validate() error {
	conn, err := c.selected()
	if err != nil {
		return err
	}
	return conn.Validate()
}

// PoolConfig holds database/sql connection pool settings. Any field left at 0
// is skipped so the database/sql default applies.
type PoolConfig struct {
	MaxIdleConns           int `yaml:"maxIdleConns"`
	MaxOpenConns           int `yaml:"maxOpenConns"`
	MaxConnLifetimeSeconds int `yaml:"maxConnLifetimeSeconds"`
}

func (p PoolConfig) apply(db *sql.DB) {
	if p.MaxIdleConns > 0 {
		db.SetMaxIdleConns(p.MaxIdleConns)
	}
	if p.MaxOpenConns > 0 {
		db.SetMaxOpenConns(p.MaxOpenConns)
	}
	if p.MaxConnLifetimeSeconds > 0 {
		db.SetConnMaxLifetime(time.Duration(p.MaxConnLifetimeSeconds) * time.Second)
	}
}

// PostgresConfig holds PostgreSQL connection configuration.
type PostgresConfig struct {
	Host     string     `yaml:"host"`
	Port     int        `yaml:"port"`
	User     string     `yaml:"user"`
	Password string     `yaml:"password"`
	Name     string     `yaml:"name"`
	SSLMode  string     `yaml:"sslMode"`
	Pool     PoolConfig `yaml:"pool"`
}

func (c PostgresConfig) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("postgres host is required")
	}
	if c.User == "" {
		return fmt.Errorf("postgres user is required")
	}
	if c.Password == "" {
		return fmt.Errorf("postgres password is required")
	}
	if c.Name == "" {
		return fmt.Errorf("postgres name is required")
	}
	return nil
}

// DSN returns the PostgreSQL connection string.
func (c PostgresConfig) DSN() string {
	// Using the URL format is more robust for handling special characters in passwords.
	// format: postgres://user:password@host:port/dbname?sslmode=disable
	host := c.Host
	if c.Port != 0 {
		host = fmt.Sprintf("%s:%d", c.Host, c.Port)
	}
	path := c.Name
	if path != "" && path[0] != '/' {
		path = "/" + path
	}
	dsn := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   host,
		Path:   path,
	}
	query := dsn.Query()
	query.Add("sslmode", c.SSLMode)
	dsn.RawQuery = query.Encode()
	return dsn.String()
}

func (c PostgresConfig) poolConfig() PoolConfig { return c.Pool }

func (c PostgresConfig) logArgs() []any {
	return []any{"host", c.Host, "port", c.Port, "database", c.Name}
}

// MySQLConfig holds MySQL connection configuration.
type MySQLConfig struct {
	Host     string     `yaml:"host"`
	Port     int        `yaml:"port"`
	User     string     `yaml:"user"`
	Password string     `yaml:"password"`
	Name     string     `yaml:"name"`
	Pool     PoolConfig `yaml:"pool"`
}

func (c MySQLConfig) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("mysql host is required")
	}
	if c.User == "" {
		return fmt.Errorf("mysql user is required")
	}
	if c.Password == "" {
		return fmt.Errorf("mysql password is required")
	}
	if c.Name == "" {
		return fmt.Errorf("mysql name is required")
	}
	return nil
}

// DSN returns the MySQL connection string.
func (c MySQLConfig) DSN() string {
	// format: user:password@tcp(host:port)/dbname?parseTime=true
	// The driver splits on the last '@' and '/', so the password needs no escaping.
	// parseTime makes DATE/DATETIME columns scan into time.Time instead of []byte.
	addr := c.Host
	if c.Port != 0 {
		addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	}
	return fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true", c.User, c.Password, addr, c.Name)
}

func (c MySQLConfig) poolConfig() PoolConfig { return c.Pool }

func (c MySQLConfig) logArgs() []any {
	return []any{"host", c.Host, "port", c.Port, "database", c.Name}
}

// SQLiteConfig holds SQLite connection configuration.
type SQLiteConfig struct {
	// Path is the database file path, or ":memory:" for an in-memory database.
	Path string     `yaml:"path"`
	Pool PoolConfig `yaml:"pool"`
}

func (c SQLiteConfig) Validate() error {
	if c.Path == "" {
		return fmt.Errorf("sqlite path is required")
	}
	return nil
}

// DSN returns the SQLite connection string.
func (c SQLiteConfig) DSN() string { return c.Path }

func (c SQLiteConfig) poolConfig() PoolConfig { return c.Pool }

func (c SQLiteConfig) logArgs() []any { return []any{"path", c.Path} }
