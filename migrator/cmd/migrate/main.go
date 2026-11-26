// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package main

import (
	"context"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/OpenNSW/core/database"
	"github.com/OpenNSW/core/migrator"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "up", "down", "status", "generate":
	default:
		fmt.Fprintf(os.Stderr, "migrate: unknown command %q\n\n", cmd)
		usage()
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		fatalf("config: %v", err)
	}

	// generate only needs the dir, no DB connection required.
	if cmd == "generate" {
		if len(os.Args) < 3 {
			fatalf("generate requires a migration name, e.g: migrate generate create_users")
		}
		m, err := migrator.New(nil, cfg.Dir, string(cfg.DB.Driver))
		if err != nil {
			fatalf("%v", err)
		}
		if err := m.Generate(os.Args[2]); err != nil {
			fatalf("%v", err)
		}
		return
	}

	db, err := database.New(context.Background(), cfg.DB)
	if err != nil {
		fatalf("open database: %v", err)
	}
	defer db.Close() //nolint:errcheck

	m, err := migrator.New(db, cfg.Dir, string(cfg.DB.Driver))
	if err != nil {
		fatalf("%v", err)
	}

	switch cmd {
	case "up":
		err = m.Up()
	case "down":
		err = m.Down()
	case "status":
		err = m.Status()
	}

	if err != nil {
		fatalf("%v", err)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage: migrate <command>

Commands:
  up               Apply all pending migrations
  down             Roll back the last applied migration
  status           Print the applied/pending state of all migrations
  generate <name>  Create a new migration file with the next version number

Configuration is read from a YAML file (see backend/config.example.yaml for
the full schema — this command only reads its db and migrationDir fields):

Environment variables:
  CONFIG_PATH     Path to the config.yaml file (default: ./config.yaml)
`)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "migrate: "+format+"\n", args...)
	os.Exit(1)
}
