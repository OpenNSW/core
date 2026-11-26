package migrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Migration holds the parsed SQL for a single migration file.
type Migration struct {
	Version int64
	Name    string
	Up      string
	Down    string
	// UpByDriver/DownByDriver hold driver-scoped SQL from -- @postgres / --
	// @sqlite sub-blocks nested inside -- @UP / -- @DOWN, keyed by driver
	// name ("postgres" or "sqlite"). Applied in addition to Up/Down for the
	// currently connected driver; nil when the migration has no such blocks.
	UpByDriver   map[string]string
	DownByDriver map[string]string
}

// ParseFile reads a .sql migration file and extracts the -- @UP and -- @DOWN blocks.
// File names must follow the convention: <version>_<name>.sql, e.g. 000001_create_users.sql.
func ParseFile(path string) (*Migration, error) {
	base := filepath.Base(path)
	version, name, err := parseFilename(base)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	blocks, err := parseBlocks(string(content))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", base, err)
	}

	return &Migration{
		Version:      version,
		Name:         name,
		Up:           blocks.Up,
		Down:         blocks.Down,
		UpByDriver:   blocks.UpByDriver,
		DownByDriver: blocks.DownByDriver,
	}, nil
}

func parseFilename(base string) (int64, string, error) {
	name := strings.TrimSuffix(base, ".sql")
	idx := strings.Index(name, "_")
	if idx < 0 {
		return 0, "", fmt.Errorf("migration filename %q must follow the pattern <version>_<name>.sql", base)
	}
	version, err := strconv.ParseInt(name[:idx], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("migration filename %q: version prefix %q is not a number", base, name[:idx])
	}
	return version, name[idx+1:], nil
}

// blockSet holds the SQL extracted from a migration file's -- @UP / -- @DOWN
// sections, including any nested per-driver sub-blocks.
type blockSet struct {
	Up           string
	Down         string
	UpByDriver   map[string]string
	DownByDriver map[string]string
}

// parseBlocks splits file content into UP and DOWN SQL sections delimited by
// the -- @UP and -- @DOWN annotations. Within a section, a -- @postgres or
// -- @sqlite marker scopes every following line to that driver until the
// next marker (dialect or section) is encountered; entering a new -- @UP /
// -- @DOWN section resets the scope back to portable/generic. Matching is
// case-insensitive and space-insensitive (e.g. "--@up", "-- @UP", "--
// @Postgres" all match).
//
// A migration needs at least one statement somewhere under -- @UP — either
// portable or in a dialect sub-block — so a migration can be entirely
// dialect-specific (e.g. only a -- @postgres block) without a dummy portable
// statement. -- @DOWN has no such requirement; a migration may be
// irreversible.
func parseBlocks(content string) (blockSet, error) {
	var (
		section          string // "", "up", "down"
		dialect          string // "", "postgres", "sqlite"
		upLines          []string
		downLines        []string
		upDialectLines   = map[string][]string{}
		downDialectLines = map[string][]string{}
	)

	for _, line := range strings.Split(content, "\n") {
		normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(line), " ", ""))
		switch normalized {
		case "--@UP":
			section, dialect = "up", ""
			continue
		case "--@DOWN":
			section, dialect = "down", ""
			continue
		case "--@POSTGRES":
			if section == "" {
				return blockSet{}, fmt.Errorf("-- @postgres marker must appear inside an -- @UP or -- @DOWN block")
			}
			dialect = "postgres"
			continue
		case "--@SQLITE":
			if section == "" {
				return blockSet{}, fmt.Errorf("-- @sqlite marker must appear inside an -- @UP or -- @DOWN block")
			}
			dialect = "sqlite"
			continue
		}
		if strings.HasPrefix(normalized, "--@") {
			return blockSet{}, fmt.Errorf("unrecognized migration annotation %q (expected @UP, @DOWN, @postgres, or @sqlite)", strings.TrimSpace(line))
		}

		switch {
		case section == "up" && dialect == "":
			upLines = append(upLines, line)
		case section == "up":
			upDialectLines[dialect] = append(upDialectLines[dialect], line)
		case section == "down" && dialect == "":
			downLines = append(downLines, line)
		case section == "down":
			downDialectLines[dialect] = append(downDialectLines[dialect], line)
		}
	}

	bs := blockSet{
		Up:           strings.TrimSpace(strings.Join(upLines, "\n")),
		Down:         strings.TrimSpace(strings.Join(downLines, "\n")),
		UpByDriver:   joinDialectBlocks(upDialectLines),
		DownByDriver: joinDialectBlocks(downDialectLines),
	}
	if bs.Up == "" && len(bs.UpByDriver) == 0 {
		return blockSet{}, fmt.Errorf("missing -- @UP annotation")
	}

	return bs, nil
}

// joinDialectBlocks joins each driver's collected lines into a single SQL
// string, dropping drivers whose block ended up empty. Returns nil if no
// driver has any content, so Migration.UpByDriver/DownByDriver stay nil for
// files with no dialect blocks.
func joinDialectBlocks(lines map[string][]string) map[string]string {
	var out map[string]string
	for driver, ls := range lines {
		joined := strings.TrimSpace(strings.Join(ls, "\n"))
		if joined == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(lines))
		}
		out[driver] = joined
	}
	return out
}
