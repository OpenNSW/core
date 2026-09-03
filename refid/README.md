// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

# Reference ID Generation System (`refid`)

`refid` is a shared Go package in the OpenNSW system that generates structured, sequential reference IDs based on YAML configuration. It allows any NSW service to define and issue custom reference IDs without writing custom ID generation code.

## Features

- **Config-Driven**: Define ID structures for multiple Issuers and ID Types purely via YAML.
- **Typed Segments**: Concatenate `literal`, `list`, `date`, and `sequence` segments into custom ID formats.
- **Durable Counters, Pluggable Backend**: Atomic sequence increment via raw SQL (no ORM) against either the bundled PostgreSQL (`refid/store/postgres`) or SQLite (`refid/store/sqlite`) backend, or bring your own `refid.SequenceStore` implementation.
- **Flexible Resets**: Scope key templates allow counters to reset daily (`{yyyyMMdd}`), monthly (`{yyyyMM}`), yearly (`{yyyy}`), or never.
- **Fail-Fast & Side-Effect Free**: Two-pass generation validates all caller parameters before executing database side-effects.

---

## Quickstart

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/OpenNSW/core/refid"
	"github.com/OpenNSW/core/refid/store/postgres"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

func main() {
	ctx := context.Background()

	// 1. Load configuration
	cfg, err := refid.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// 2. Connect DB and migrate the sequence table
	dsn := "host=localhost port=5432 user=postgres password=postgres dbname=postgres sslmode=disable"
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("failed to open db: %v", err)
	}
	if err := postgres.Migrate(ctx, db); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	// 3. Initialize Registry
	store, err := postgres.New(db)
	if err != nil {
		log.Fatalf("failed to create store: %v", err)
	}
	reg, err := refid.NewRegistry(cfg, store)
	if err != nil {
		log.Fatalf("failed to create registry: %v", err)
	}

	// 4. Generate reference ID
	id, err := reg.Generate(ctx, "RTA", "application_id", map[string]string{
		"officeCode": "COL",
	})
	if err != nil {
		log.Fatalf("generation failed: %v", err)
	}

	fmt.Println("Generated Reference ID:", id)
	// Output: RTA-APP-COL-20260818-000001
}
```

---

## Segment Types

| Segment | Description | Key Fields | Example |
|---|---|---|---|
| `literal` | Fixed text string | `value: "FCAU-"` | `FCAU-` |
| `list` | Parameter value validated against a controlled list | `list: office_location`, `param: officeCode` | `COL` |
| `date` | Current UTC date/time using Go reference layout | `layout: "20060102"` | `20260818` |
| `sequence` | Zero-padded durable counter | `scopeKey: "{issuer}:{idType}:{officeCode}:{yyyyMMdd}"`, `padding: 6` | `000042` |

---

## Scope Key Placeholders & Reset Cadence

Sequence segments resolve a `scopeKey` template per generation call. Each unique scope key gets its own independent counter.

Reserved placeholders:
- `{issuer}` — Issuing authority (e.g. `"RTA"`)
- `{idType}` — Format identifier (e.g. `"application_id"`)
- `{yyyy}` — Current 4-digit UTC year (Yearly reset)
- `{yyyyMM}` — Current UTC year + month (Monthly reset)
- `{yyyyMMdd}` — Current UTC year + month + day (Daily reset)
- `{<param>}` — Any caller-supplied param (e.g. `{officeCode}`)

> [!NOTE]
> Curly braces `{` and `}` are reserved syntax for placeholder delimiters in `scopeKey` templates.

---

## Database Setup

`refid.SequenceStore` is a pluggable interface (`Next(ctx, scopeKey, max) (int64, error)`); the package ships two raw-SQL backends, each in its own subpackage. A program that imports only `refid` never compiles a database driver into its binary — Go's per-package import graph links `pgx` or `modernc.org/sqlite` only if you actually import `refid/store/postgres` or `refid/store/sqlite`, respectively. `go.mod` still lists both as requirements of the module as a whole, since every subpackage shares one `go.mod` for simplicity; that's a module-level dependency-graph entry, not something your binary picks up unless you import the subpackage that uses it.

### PostgreSQL (`refid/store/postgres`)

A single table (`refid_sequences` by default) with row-level atomic upsert:

```sql
CREATE TABLE IF NOT EXISTS refid_sequences (
    scope_key  TEXT        NOT NULL PRIMARY KEY,
    counter    BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Initialize it automatically via `postgres.Migrate(ctx, db)`. To use a custom table name:

```go
store, err := postgres.New(db, postgres.WithTableName("custom_sequences"))
err = postgres.Migrate(ctx, db, postgres.WithTableName("custom_sequences"))
```

`db` is a `*sql.DB` opened against the `pgx` driver (`sql.Open("pgx", dsn)`) — see the Quickstart above.

### SQLite (`refid/store/sqlite`)

Same schema shape and API, using the pure-Go `modernc.org/sqlite` driver (no CGO):

```go
db, err := sql.Open("sqlite", "refid.db") // registered by importing github.com/OpenNSW/core/refid/store/sqlite
if err := sqlite.Migrate(ctx, db); err != nil { ... }
store, err := sqlite.New(db)
```

SQLite allows only one writer at a time, and nothing sets a busy timeout by default, so
concurrent access can fail immediately with `SQLITE_BUSY`. Set a busy timeout in the DSN
(e.g. `sql.Open("sqlite", "file:refid.db?_busy_timeout=5000")`) if you need `Next` to wait
instead of failing, or use `refid/store/postgres` for real concurrent-safe access. This
makes SQLite a convenient choice for local development and tests.

### Bring your own backend

Any type implementing `refid.SequenceStore` works — a Redis-backed counter, an in-memory store for tests, etc. Whether `Next` is safe under concurrent or multi-process use is entirely up to your implementation; the two bundled backends above sit at different points on that spectrum, so check their docs for what each one actually guarantees.

---

## Error Handling

Check sentinel errors using `errors.Is(err, refid.Err...)`:

- `refid.ErrUnknownIssuer` — Issuer not configured in registry.
- `refid.ErrUnknownIDType` — ID Type not found under specified issuer.
- `refid.ErrInvalidParam` — Required param missing, not in allowed list, or scopeKey placeholder un-substituted.
- `refid.ErrCounterOverflow` — Sequence counter value exceeds configured `padding` width.
