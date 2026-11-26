package migrator

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Migrator applies and tracks SQL migrations from a directory of .sql files.
type Migrator struct {
	db     *sql.DB
	dir    string
	driver string // "sqlite" or "postgres"
}

type migrationRecord struct {
	version   int64
	name      string
	appliedAt time.Time
}

// New creates a Migrator backed by db, reading .sql files from dir.
// driver must be "sqlite" or "postgres".
func New(db *sql.DB, dir string, driver string) (*Migrator, error) {
	if driver != "sqlite" && driver != "postgres" {
		return nil, fmt.Errorf("invalid migration driver %q (must be 'sqlite' or 'postgres')", driver)
	}
	return &Migrator{db: db, dir: dir, driver: driver}, nil
}

// Up applies all pending migrations in version order.
func (m *Migrator) Up() error {
	if err := m.initTable(); err != nil {
		return fmt.Errorf("init tracking table: %w", err)
	}
	applied, err := m.appliedMigrations()
	if err != nil {
		return err
	}
	migrations, err := m.loadFiles()
	if err != nil {
		return err
	}

	if err := validateSequence(migrations); err != nil {
		return err
	}

	count := 0
	for _, mg := range migrations {
		if _, ok := applied[mg.Version]; ok {
			continue
		}
		if err := m.apply(mg); err != nil {
			return fmt.Errorf("applying %06d_%s: %w", mg.Version, mg.Name, err)
		}
		fmt.Printf("applied  %06d_%s\n", mg.Version, mg.Name)
		count++
	}
	if count == 0 {
		fmt.Println("no pending migrations")
	}
	return nil
}

// Down rolls back the most recently applied migration.
func (m *Migrator) Down() error {
	if err := m.initTable(); err != nil {
		return fmt.Errorf("init tracking table: %w", err)
	}
	applied, err := m.appliedMigrations()
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Println("no migrations to roll back")
		return nil
	}
	migrations, err := m.loadFiles()
	if err != nil {
		return err
	}

	var last *Migration
	for i := len(migrations) - 1; i >= 0; i-- {
		if _, ok := applied[migrations[i].Version]; ok {
			last = migrations[i]
			break
		}
	}
	if last == nil {
		fmt.Println("no migrations to roll back")
		return nil
	}
	if last.Down == "" && last.DownByDriver[m.driver] == "" {
		return fmt.Errorf("migration %06d_%s has no -- @DOWN block for driver %q", last.Version, last.Name, m.driver)
	}
	if err := m.rollback(last); err != nil {
		return fmt.Errorf("rolling back %06d_%s: %w", last.Version, last.Name, err)
	}
	fmt.Printf("rolled back %06d_%s\n", last.Version, last.Name)
	return nil
}

// Status prints each migration file with its applied / pending state.
func (m *Migrator) Status() error {
	if err := m.initTable(); err != nil {
		return fmt.Errorf("init tracking table: %w", err)
	}
	applied, err := m.appliedMigrations()
	if err != nil {
		return err
	}
	migrations, err := m.loadFiles()
	if err != nil {
		return err
	}

	fmt.Printf("%-10s %-40s %-10s %s\n", "VERSION", "NAME", "STATUS", "APPLIED AT")
	fmt.Println(strings.Repeat("-", 75))
	for _, mg := range migrations {
		if r, ok := applied[mg.Version]; ok {
			fmt.Printf("%-10d %-40s %-10s %s\n", mg.Version, mg.Name, "applied", r.appliedAt.Format(time.RFC3339))
		} else {
			fmt.Printf("%-10d %-40s %-10s\n", mg.Version, mg.Name, "pending")
		}
	}
	if len(migrations) == 0 {
		fmt.Println("no migration files found in", m.dir)
	}
	return nil
}

func (m *Migrator) initTable() error {
	var q string
	if m.driver == "postgres" {
		q = `CREATE TABLE IF NOT EXISTS __migrations (
			version    BIGINT      PRIMARY KEY,
			name       TEXT        NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL
		)`
	} else {
		q = `CREATE TABLE IF NOT EXISTS __migrations (
			version    INTEGER  PRIMARY KEY,
			name       TEXT     NOT NULL,
			applied_at DATETIME NOT NULL
		)`
	}
	_, err := m.db.Exec(q)
	return err
}

func (m *Migrator) appliedMigrations() (map[int64]migrationRecord, error) {
	rows, err := m.db.Query(`SELECT version, name, applied_at FROM __migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("querying applied migrations: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	records := make(map[int64]migrationRecord)
	for rows.Next() {
		var r migrationRecord
		if err := rows.Scan(&r.version, &r.name, &r.appliedAt); err != nil {
			return nil, fmt.Errorf("scanning migration record: %w", err)
		}
		records[r.version] = r
	}
	return records, rows.Err()
}

func (m *Migrator) loadFiles() ([]*Migration, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, fmt.Errorf("reading migration dir %s: %w", m.dir, err)
	}

	var migrations []*Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		mg, err := ParseFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", e.Name(), err)
		}
		migrations = append(migrations, mg)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	for i := 1; i < len(migrations); i++ {
		if migrations[i].Version == migrations[i-1].Version {
			return nil, fmt.Errorf("duplicate migration version %d: %s and %s",
				migrations[i].Version, migrations[i-1].Name, migrations[i].Name)
		}
	}
	return migrations, nil
}

func (m *Migrator) apply(mg *Migration) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if err := execStatements(tx, mg.Up); err != nil {
		return err
	}
	if extra, ok := mg.UpByDriver[m.driver]; ok {
		if err := execStatements(tx, extra); err != nil {
			return err
		}
	}

	var insertQ string
	if m.driver == "postgres" {
		insertQ = `INSERT INTO __migrations (version, name, applied_at) VALUES ($1, $2, $3)`
	} else {
		insertQ = `INSERT INTO __migrations (version, name, applied_at) VALUES (?, ?, ?)`
	}
	if _, err := tx.Exec(insertQ, mg.Version, mg.Name, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Migrator) rollback(mg *Migration) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Unwind in the reverse order of apply: the driver-specific addition
	// (e.g. an index) goes first, then the portable base change it depends on.
	if extra, ok := mg.DownByDriver[m.driver]; ok {
		if err := execStatements(tx, extra); err != nil {
			return err
		}
	}
	if err := execStatements(tx, mg.Down); err != nil {
		return err
	}

	var deleteQ string
	if m.driver == "postgres" {
		deleteQ = `DELETE FROM __migrations WHERE version = $1`
	} else {
		deleteQ = `DELETE FROM __migrations WHERE version = ?`
	}
	if _, err := tx.Exec(deleteQ, mg.Version); err != nil {
		return err
	}
	return tx.Commit()
}

// Generate creates a new migration file in dir with the next version number.
// The file name follows the pattern <version>_<name>.sql and is seeded with
// -- @UP and -- @DOWN block stubs.
func (m *Migrator) Generate(name string) error {
	migrations, err := m.loadFiles()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading migration dir: %w", err)
	}

	var next int64 = 1
	if len(migrations) > 0 {
		next = migrations[len(migrations)-1].Version + 1
	}

	filename := fmt.Sprintf("%06d_%s.sql", next, name)
	path := filepath.Join(m.dir, filename)

	content := fmt.Sprintf(
		"-- Created at: %s\n"+
			"-- Need Postgres- or SQLite-only SQL? Nest -- @postgres / -- @sqlite blocks\n"+
			"-- inside @UP or @DOWN -- see docs/migrations.md.\n\n"+
			"-- @UP\n\n\n-- @DOWN\n\n",
		time.Now().UTC().Format(time.RFC3339),
	)
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return fmt.Errorf("creating migration dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}

	fmt.Println("created", path)
	return nil
}

// validateSequence ensures migration versions form a contiguous sequence with no gaps.
func validateSequence(migrations []*Migration) error {
	for i := 1; i < len(migrations); i++ {
		if migrations[i].Version != migrations[i-1].Version+1 {
			return fmt.Errorf("gap in migration versions: missing version between %d (%s) and %d (%s)",
				migrations[i-1].Version, migrations[i-1].Name,
				migrations[i].Version, migrations[i].Name)
		}
	}
	return nil
}

// execStatements executes the SQL block in tx. Both pgx and modernc.org/sqlite
// support multiple statements in a single Exec call, which avoids the fragility
// of splitting on ";" (semicolons can appear inside string literals or comments).
func execStatements(tx *sql.Tx, sql string) error {
	if s := strings.TrimSpace(sql); s != "" {
		_, err := tx.Exec(s)
		return err
	}
	return nil
}
