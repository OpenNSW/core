package migrator

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestMigrator(t *testing.T, files map[string]string) (*Migrator, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	m, err := New(db, dir, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	return m, db
}

func TestUp_AppliesPendingMigrations(t *testing.T) {
	m, db := newTestMigrator(t, map[string]string{
		"000001_create_foo.sql": "-- @UP\nCREATE TABLE foo (id INTEGER PRIMARY KEY);\n-- @DOWN\nDROP TABLE foo;",
		"000002_create_bar.sql": "-- @UP\nCREATE TABLE bar (id INTEGER PRIMARY KEY);\n-- @DOWN\nDROP TABLE bar;",
	})

	if err := m.Up(); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM __migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("__migrations count = %d, want 2", count)
	}
}

func TestUp_IdempotentOnSecondCall(t *testing.T) {
	m, db := newTestMigrator(t, map[string]string{
		"000001_create_foo.sql": "-- @UP\nCREATE TABLE foo (id INTEGER PRIMARY KEY);\n-- @DOWN\nDROP TABLE foo;",
	})

	if err := m.Up(); err != nil {
		t.Fatalf("first Up() error = %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("second Up() error = %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM __migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("__migrations count = %d, want 1 (idempotent)", count)
	}
}

func TestDown_RollsBackLastMigration(t *testing.T) {
	m, db := newTestMigrator(t, map[string]string{
		"000001_create_foo.sql": "-- @UP\nCREATE TABLE foo (id INTEGER PRIMARY KEY);\n-- @DOWN\nDROP TABLE foo;",
		"000002_create_bar.sql": "-- @UP\nCREATE TABLE bar (id INTEGER PRIMARY KEY);\n-- @DOWN\nDROP TABLE bar;",
	})

	if err := m.Up(); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := m.Down(); err != nil {
		t.Fatalf("Down() error = %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM __migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("__migrations count = %d, want 1 after rollback", count)
	}

	// bar table must be gone; foo must still exist
	if _, err := db.Exec(`INSERT INTO foo VALUES (1)`); err != nil {
		t.Errorf("foo table should still exist: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO bar VALUES (1)`); err == nil {
		t.Error("bar table should have been dropped")
	}
}

func TestDown_NoMigrations(t *testing.T) {
	m, _ := newTestMigrator(t, map[string]string{})
	if err := m.Down(); err != nil {
		t.Fatalf("Down() on empty state returned error: %v", err)
	}
}

func TestStatus_NoError(t *testing.T) {
	m, _ := newTestMigrator(t, map[string]string{
		"000001_create_foo.sql": "-- @UP\nCREATE TABLE foo (id INTEGER PRIMARY KEY);\n-- @DOWN\nDROP TABLE foo;",
	})
	if err := m.Up(); err != nil {
		t.Fatal(err)
	}
	if err := m.Status(); err != nil {
		t.Fatalf("Status() error = %v", err)
	}
}

func TestUp_ErrorsOnGappedVersions(t *testing.T) {
	m, _ := newTestMigrator(t, map[string]string{
		"000001_create_foo.sql": "-- @UP\nCREATE TABLE foo (id INTEGER PRIMARY KEY);\n-- @DOWN\nDROP TABLE foo;",
		"000003_create_bar.sql": "-- @UP\nCREATE TABLE bar (id INTEGER PRIMARY KEY);\n-- @DOWN\nDROP TABLE bar;",
	})

	if err := m.Up(); err == nil {
		t.Fatal("Up() expected error for gap in versions, got nil")
	}
}

func TestUp_SkipsDialectSubBlockForOtherDrivers(t *testing.T) {
	// PRAGMA table_info is SQLite-only; the -- @postgres sub-block below
	// would fail under postgres, but this migrator runs against sqlite, so
	// only the generic block and the -- @sqlite sub-block should execute.
	m, db := newTestMigrator(t, map[string]string{
		"000001_create_foo.sql": "-- @UP\nCREATE TABLE foo (id INTEGER PRIMARY KEY);\n\n" +
			"-- @sqlite\nCREATE INDEX idx_foo_id ON foo (id);\n\n" +
			"-- @postgres\nCREATE INDEX idx_foo_id ON foo USING GIN (id);\n\n" +
			"-- @DOWN\nDROP TABLE foo;",
	})

	if err := m.Up(); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	var indexCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_foo_id'`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Errorf("idx_foo_id count = %d, want 1 (sqlite sub-block should have run)", indexCount)
	}
}

func TestUpDown_DialectOnlyMigration(t *testing.T) {
	// Both @UP and @DOWN have no portable statement — everything lives in a
	// -- @sqlite sub-block. This must still apply and roll back cleanly.
	m, db := newTestMigrator(t, map[string]string{
		"000001_sqlite_only.sql": "-- @UP\n\n-- @sqlite\nCREATE TABLE foo (id INTEGER PRIMARY KEY);\n\n" +
			"-- @DOWN\n\n-- @sqlite\nDROP TABLE foo;",
	})

	if err := m.Up(); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO foo VALUES (1)`); err != nil {
		t.Fatalf("foo table should exist after a dialect-only up: %v", err)
	}

	if err := m.Down(); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO foo VALUES (1)`); err == nil {
		t.Error("foo table should have been dropped by the dialect-only down block")
	}
}

func TestDown_RunsDialectSubBlockBeforeGeneric(t *testing.T) {
	m, db := newTestMigrator(t, map[string]string{
		"000001_create_log.sql": "-- @UP\nCREATE TABLE rollback_log (seq INTEGER PRIMARY KEY AUTOINCREMENT, step TEXT);\n" +
			"-- @DOWN\nDROP TABLE rollback_log;",
		"000002_ordered_down.sql": "-- @UP\nCREATE TABLE foo (id INTEGER PRIMARY KEY);\n\n" +
			"-- @DOWN\nINSERT INTO rollback_log (step) VALUES ('generic');\n\n" +
			"-- @sqlite\nINSERT INTO rollback_log (step) VALUES ('dialect');",
	})

	if err := m.Up(); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := m.Down(); err != nil {
		t.Fatalf("Down() error = %v", err)
	}

	rows, err := db.Query(`SELECT step FROM rollback_log ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var steps []string
	for rows.Next() {
		var step string
		if err := rows.Scan(&step); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, step)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	want := []string{"dialect", "generic"}
	if !reflect.DeepEqual(steps, want) {
		t.Errorf("rollback order = %v, want %v (the driver-specific sub-block must run before the generic block)", steps, want)
	}
}

func TestUp_RollsBackOnFailure(t *testing.T) {
	m, db := newTestMigrator(t, map[string]string{
		"000001_create_foo.sql": "-- @UP\nCREATE TABLE foo (id INTEGER PRIMARY KEY);\nINVALID SQL HERE;\n-- @DOWN\nDROP TABLE foo;",
	})

	if err := m.Up(); err == nil {
		t.Fatal("Up() expected error for invalid SQL, got nil")
	}

	// foo table must not exist — transaction was rolled back
	if _, err := db.Exec(`INSERT INTO foo VALUES (1)`); err == nil {
		t.Error("foo table should not exist after failed migration")
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM __migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("__migrations count = %d, want 0 after failed migration", count)
	}
}
