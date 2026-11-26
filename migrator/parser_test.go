package migrator

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseFilename(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantVersion int64
		wantName    string
		wantErr     bool
	}{
		{"valid", "000001_create_users.sql", 1, "create_users", false},
		{"multi-underscore", "000002_create_application_table.sql", 2, "create_application_table", false},
		{"no underscore", "create_users.sql", 0, "", true},
		{"non-numeric version", "abc_create_users.sql", 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, name, err := parseFilename(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseFilename(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr {
				if version != tt.wantVersion {
					t.Errorf("version = %d, want %d", version, tt.wantVersion)
				}
				if name != tt.wantName {
					t.Errorf("name = %q, want %q", name, tt.wantName)
				}
			}
		})
	}
}

func TestParseBlocks(t *testing.T) {
	tests := []struct {
		name             string
		content          string
		wantUp           string
		wantDown         string
		wantUpByDriver   map[string]string
		wantDownByDriver map[string]string
		wantErr          bool
	}{
		{
			name: "up and down",
			content: `-- @UP
CREATE TABLE users (id INTEGER PRIMARY KEY);

-- @DOWN
DROP TABLE users;`,
			wantUp:   "CREATE TABLE users (id INTEGER PRIMARY KEY);",
			wantDown: "DROP TABLE users;",
		},
		{
			name: "up only",
			content: `-- @UP
CREATE TABLE users (id INTEGER PRIMARY KEY);`,
			wantUp:   "CREATE TABLE users (id INTEGER PRIMARY KEY);",
			wantDown: "",
		},
		{
			name:    "missing up",
			content: `-- @DOWN\nDROP TABLE users;`,
			wantErr: true,
		},
		{
			name:     "no space variant --@UP",
			content:  "--@UP\nCREATE TABLE users (id INTEGER PRIMARY KEY);\n--@DOWN\nDROP TABLE users;",
			wantUp:   "CREATE TABLE users (id INTEGER PRIMARY KEY);",
			wantDown: "DROP TABLE users;",
		},
		{
			name:     "lowercase variant",
			content:  "-- @up\nCREATE TABLE users (id INTEGER PRIMARY KEY);\n-- @down\nDROP TABLE users;",
			wantUp:   "CREATE TABLE users (id INTEGER PRIMARY KEY);",
			wantDown: "DROP TABLE users;",
		},
		{
			name: "nested postgres sub-block in up and down",
			content: `-- @UP
ALTER TABLE users ADD COLUMN custom_data JSONB;

-- @postgres
CREATE INDEX IF NOT EXISTS idx_users_custom_data ON users USING GIN (custom_data);

-- @DOWN
ALTER TABLE users DROP COLUMN custom_data;

-- @postgres
DROP INDEX IF EXISTS idx_users_custom_data;`,
			wantUp:   "ALTER TABLE users ADD COLUMN custom_data JSONB;",
			wantDown: "ALTER TABLE users DROP COLUMN custom_data;",
			wantUpByDriver: map[string]string{
				"postgres": "CREATE INDEX IF NOT EXISTS idx_users_custom_data ON users USING GIN (custom_data);",
			},
			wantDownByDriver: map[string]string{
				"postgres": "DROP INDEX IF EXISTS idx_users_custom_data;",
			},
		},
		{
			name:     "sqlite sub-block, lowercase and no-space variants",
			content:  "--@UP\nCREATE TABLE foo (id INTEGER PRIMARY KEY);\n-- @sqlite\nPRAGMA foreign_keys=ON;\n--@DOWN\nDROP TABLE foo;",
			wantUp:   "CREATE TABLE foo (id INTEGER PRIMARY KEY);",
			wantDown: "DROP TABLE foo;",
			wantUpByDriver: map[string]string{
				"sqlite": "PRAGMA foreign_keys=ON;",
			},
		},
		{
			name: "dialect marker outside any section",
			content: `-- @postgres
CREATE INDEX foo ON bar (baz);

-- @UP
CREATE TABLE bar (id INTEGER PRIMARY KEY);`,
			wantErr: true,
		},
		{
			name: "unrecognized annotation",
			content: `-- @UP
CREATE TABLE foo (id INTEGER PRIMARY KEY);

-- @mysql
CREATE INDEX foo ON foo (id);`,
			wantErr: true,
		},
		{
			name: "dialect-only up with no portable statement",
			content: `-- @UP

-- @postgres
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- @DOWN

-- @postgres
DROP EXTENSION IF EXISTS pgcrypto;`,
			wantUp:   "",
			wantDown: "",
			wantUpByDriver: map[string]string{
				"postgres": "CREATE EXTENSION IF NOT EXISTS pgcrypto;",
			},
			wantDownByDriver: map[string]string{
				"postgres": "DROP EXTENSION IF EXISTS pgcrypto;",
			},
		},
		{
			name: "up with no content anywhere still errors",
			content: `-- @UP

-- @DOWN
DROP TABLE foo;`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs, err := parseBlocks(tt.content)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseBlocks() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if bs.Up != tt.wantUp {
				t.Errorf("up = %q, want %q", bs.Up, tt.wantUp)
			}
			if bs.Down != tt.wantDown {
				t.Errorf("down = %q, want %q", bs.Down, tt.wantDown)
			}
			if !reflect.DeepEqual(bs.UpByDriver, tt.wantUpByDriver) {
				t.Errorf("upByDriver = %#v, want %#v", bs.UpByDriver, tt.wantUpByDriver)
			}
			if !reflect.DeepEqual(bs.DownByDriver, tt.wantDownByDriver) {
				t.Errorf("downByDriver = %#v, want %#v", bs.DownByDriver, tt.wantDownByDriver)
			}
		})
	}
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "000001_create_users.sql")
	content := "-- @UP\nCREATE TABLE users (id INTEGER PRIMARY KEY);\n\n-- @DOWN\nDROP TABLE users;\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	mg, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if mg.Version != 1 {
		t.Errorf("Version = %d, want 1", mg.Version)
	}
	if mg.Name != "create_users" {
		t.Errorf("Name = %q, want create_users", mg.Name)
	}
	if mg.Up == "" {
		t.Error("Up block is empty")
	}
	if mg.Down == "" {
		t.Error("Down block is empty")
	}
}
