// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package database

import (
	"fmt"
	"net/url"
)

// LogLevel controls how verbose GORM's query logging is.
type LogLevel int

const (
	// LogSilent disables all GORM query logging.
	LogSilent LogLevel = iota
	// LogError logs only errors (default).
	LogError
	// LogWarn logs errors and slow queries.
	LogWarn
	// LogInfo logs every SQL query. Useful for development but noisy in production.
	LogInfo
)

// Config holds database connection configuration.
type Config struct {
	Host                   string `yaml:"host"`
	Port                   int    `yaml:"port"`
	Username               string `yaml:"username"`
	Password               string `yaml:"password"`
	Name                   string `yaml:"name"`
	SSLMode                string `yaml:"sslMode"`
	MaxIdleConns           int    `yaml:"maxIdleConns"`
	MaxOpenConns           int    `yaml:"maxOpenConns"`
	MaxConnLifetimeSeconds int    `yaml:"maxConnLifetimeSeconds"`

	// LogLevel controls GORM query logging verbosity.
	// Defaults to LogError when not set (zero value).
	LogLevel LogLevel `yaml:"logLevel"`
}

func (c Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("database host is required")
	}
	if c.Username == "" {
		return fmt.Errorf("database username is required")
	}
	if c.Password == "" {
		return fmt.Errorf("database password is required")
	}
	if c.Name == "" {
		return fmt.Errorf("database name is required")
	}
	return nil
}

// DSN returns the database connection string.
func (c Config) DSN() string {
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
		User:   url.UserPassword(c.Username, c.Password),
		Host:   host,
		Path:   path,
	}
	query := dsn.Query()
	query.Add("sslmode", c.SSLMode)
	dsn.RawQuery = query.Encode()
	return dsn.String()
}
