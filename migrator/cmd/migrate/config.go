// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package main

import (
	"fmt"
	"os"

	"github.com/OpenNSW/core/configyaml"
	"github.com/OpenNSW/core/database"
)

// defaultConfigPath is where LoadConfig looks for its YAML file when
// CONFIG_PATH is unset. It can be the application's own config file: the
// migrate command reads only its db and migrationDir fields.
const defaultConfigPath = "./config.yaml"

// Config holds all configuration for the migrate command.
type Config struct {
	DB database.Config `yaml:"db"`
	// Dir is the path to the SQL migration files directory.
	Dir string `yaml:"migrationDir"`
}

// LoadConfig loads configuration from the YAML file at CONFIG_PATH (default
// "./config.yaml"), resolving "{{env:NAME}}" / "{{file:/path}}" placeholders
// with configyaml.LoadAndExpand. The db section has the shape of
// database.Config, so an application and its migrations can share one file.
func LoadConfig() (Config, error) {
	path := envOrDefault("CONFIG_PATH", defaultConfigPath)

	var cfg Config
	if err := configyaml.LoadAndExpand(path, &cfg); err != nil {
		return Config{}, err
	}

	if cfg.DB.Driver == "" {
		cfg.DB.Driver = database.SQLite
	}
	if cfg.DB.SQLite == nil {
		cfg.DB.SQLite = &database.SQLiteConfig{}
	}
	if cfg.DB.SQLite.Path == "" {
		cfg.DB.SQLite.Path = "./agency_applications.db"
	}
	if cfg.DB.Postgres != nil && cfg.DB.Postgres.SSLMode == "" {
		cfg.DB.Postgres.SSLMode = "require"
	}
	if cfg.Dir == "" {
		cfg.Dir = "./migrations"
	}

	if cfg.DB.Driver != database.SQLite && cfg.DB.Driver != database.Postgres {
		return Config{}, fmt.Errorf("unsupported database driver %q (must be %q or %q)", cfg.DB.Driver, database.SQLite, database.Postgres)
	}
	if err := cfg.DB.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func envOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
