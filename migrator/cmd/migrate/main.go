package main

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	// "sqlite" is registered transitively via internal/database (glebarez/sqlite);
	// a direct import here would double-register the driver and panic.
	"github.com/OpenNSW/agency/backend/internal/database"
	"github.com/OpenNSW/agency/backend/internal/migrator"
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
		m, err := migrator.New(nil, cfg.Dir, cfg.DB.Driver)
		if err != nil {
			fatalf("%v", err)
		}
		if err := m.Generate(os.Args[2]); err != nil {
			fatalf("%v", err)
		}
		return
	}

	db, err := openDB(cfg.DB)
	if err != nil {
		fatalf("open database: %v", err)
	}
	defer db.Close() //nolint:errcheck

	m, err := migrator.New(db, cfg.Dir, cfg.DB.Driver)
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

func openDB(cfg database.Config) (*sql.DB, error) {
	var (
		db  *sql.DB
		err error
	)
	switch cfg.Driver {
	case "sqlite":
		db, err = sql.Open("sqlite", cfg.SQLite.Path)
	case "postgres":
		pg := cfg.Postgres
		u := &url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(pg.User, pg.Password),
			Host:     net.JoinHostPort(pg.Host, pg.Port),
			Path:     "/" + pg.Name,
			RawQuery: "sslmode=" + url.QueryEscape(pg.SSLMode),
		}
		db, err = sql.Open("pgx", u.String())
	default:
		return nil, fmt.Errorf("unsupported driver %q", cfg.Driver)
	}
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("database unreachable: %w", err)
	}
	return db, nil
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
