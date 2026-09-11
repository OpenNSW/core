// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

# Reference ID Generation System (`refid`)

`refid` is a shared Go package in the OpenNSW system that generates structured, sequential reference IDs based on YAML configuration. It allows any NSW service to define and issue custom reference IDs without writing custom ID generation code.

## Features

- **Config-Driven**: Define ID structures for multiple Issuers and ID Types purely via YAML.
- **Typed Segments**: Concatenate `literal`, `list`, `date`, `sequence`, and `random` segments into custom ID formats.
- **Durable Counters, Pluggable Backend**: Atomic sequence increment via raw SQL (no ORM) against either the bundled PostgreSQL (`refid/store/postgres`) or SQLite (`refid/store/sqlite`) backend, or bring your own `refid.SequenceStore` implementation.
- **Random Segments, Collision-Checked**: Fixed-length random values (numeric/alpha/alphanumeric) reserved via a pluggable `refid.RandomStore`, retrying on collision.
- **Flexible Resets**: Scope key templates allow counters (and random value uniqueness sets) to reset daily (`{yyyyMMdd}`), monthly (`{yyyyMM}`), yearly (`{yyyy}`), or never.
- **Fail-Fast & Side-Effect Free**: Two-pass generation validates all caller parameters before executing database side-effects, and a format is capped at one stateful (sequence/random) segment so a later segment's failure can never orphan an earlier one's already-committed side effect.

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
	if err := postgres.MigrateSequence(ctx, db); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	// 3. Initialize Registry. WithSequenceStore/WithRandomStore are both
	// optional — supply only the store(s) backing segment types your config
	// actually uses (this example's config has no random segments).
	store, err := postgres.NewSequence(db)
	if err != nil {
		log.Fatalf("failed to create store: %v", err)
	}
	reg, err := refid.NewRegistry(cfg, refid.WithSequenceStore(store))
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
| `sequence` | Zero-padded durable counter | `sequence: {scopeKey: "{issuer}:{idType}:{officeCode}:{yyyyMMdd}", padding: 6}` | `000042` |
| `random` | Fixed-length random value, collision-checked via `RandomStore` | `random: {scopeKey: "{issuer}:{idType}", charset: alphanumeric, length: 8}` | `7K2QQXAB` |

`sequence` and `random` are each configured under their own nested block (`SequenceSegmentConfig`/`RandomSegmentConfig`) rather than flat fields on the segment, and a format may contain at most one of them combined — see [Stateful Segment Limit](#stateful-segment-limit) below.

### `random` fields

| Field | Description |
|---|---|
| `scopeKey` | Same template syntax as `sequence` — determines the uniqueness scope for generated values. |
| `charset` | One of `numeric`, `alpha`, `alphanumeric`. |
| `length` | Number of characters to generate (must be ≥ 1). |
| `maxAttempts` | Collision retries before `Generate` returns `ErrRandomExhausted`. Optional; defaults to 10, must be between 0 and 100. |

---

## Scope Key Placeholders & Reset Cadence

Sequence and random segments each resolve a `scopeKey` template per generation call — for `sequence` it scopes a durable counter, for `random` it scopes the set of previously issued values checked for collisions. Each unique scope key gets its own independent counter or uniqueness set.

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

## Stateful Segment Limit

`sequence` and `random` are the only segment types with a side effect that persists to a store (a counter increment, a random value reservation) — `literal`/`list`/`date` are pure functions of the caller's params and the current time. `Generate` validates every segment first, then renders them in order with no rollback: if a format had two or more stateful segments and a later one failed during render (a sequence overflowing, a random segment exhausting its retries), an earlier one's already-committed side effect would be permanently orphaned — for a random segment, that permanently wastes one value from its bounded charset/length space with no ID ever returned.

To rule this out, `NewRegistry` rejects any format with more than one `sequence`/`random` segment combined. A format can still mix any number of `literal`/`list`/`date` segments with at most one of `sequence` or `random`.

---

## Database Setup

`refid.SequenceStore` (`Next(ctx, scopeKey, max) (int64, error)`) and `refid.RandomStore` (`Reserve(ctx, scopeKey, value) error`) are pluggable interfaces; the package ships raw-SQL backends for both, each in its own subpackage. Wire in `SequenceStore` via `refid.WithSequenceStore`, `RandomStore` via `refid.WithRandomStore` — both optional, needed only if your config uses the corresponding segment type.

Neither backend registers a `database/sql` driver — they only issue SQL against the `*sql.DB` you hand them, so you import the driver, open the connection, and pass the result in. That keeps the driver choice yours, and avoids an `init` panic in a binary that already registers the same driver name.

### PostgreSQL (`refid/store/postgres`)

`SequenceStore` uses a single table (`refid_sequences` by default) with row-level atomic upsert:

```sql
CREATE TABLE IF NOT EXISTS refid_sequences (
    scope_key  TEXT        NOT NULL PRIMARY KEY,
    counter    BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Initialize it automatically via `postgres.MigrateSequence(ctx, db)`. To use a custom table name:

```go
store, err := postgres.NewSequence(db, postgres.WithTableName("custom_sequences"))
err = postgres.MigrateSequence(ctx, db, postgres.WithTableName("custom_sequences"))
```

`db` is a `*sql.DB` you opened yourself — the queries use PostgreSQL's native `$1` placeholders, so any PostgreSQL driver works. The Quickstart above uses pgx.

`RandomStore` uses a similarly shaped table (`refid_random` by default), keyed on `(scope_key, value)` rather than incrementing a counter — every random-segment format shares this one table, distinguished by `scope_key`:

```sql
CREATE TABLE IF NOT EXISTS refid_random (
    scope_key  TEXT        NOT NULL,
    value      TEXT        NOT NULL,
    issued_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (scope_key, value)
);
```

```go
randomStore, err := postgres.NewRandom(db, postgres.WithTableName("custom_random"))
err = postgres.MigrateRandom(ctx, db, postgres.WithTableName("custom_random"))
```

### SQLite (`refid/store/sqlite`)

Same schema shapes and API for both `SequenceStore` and `RandomStore`. Import a SQLite driver — `modernc.org/sqlite` is pure Go, no CGO:

```go
import _ "modernc.org/sqlite" // registers the "sqlite" driver

db, err := sql.Open("sqlite", "refid.db")
if err := sqlite.MigrateSequence(ctx, db); err != nil { ... }
store, err := sqlite.NewSequence(db)

if err := sqlite.MigrateRandom(ctx, db); err != nil { ... }
randomStore, err := sqlite.NewRandom(db)
```

SQLite allows only one writer at a time, and nothing sets a busy timeout by default, so
concurrent access can fail immediately with `SQLITE_BUSY`. Set a busy timeout in the DSN
(e.g. `sql.Open("sqlite", "file:refid.db?_busy_timeout=5000")`) if you need `Next`/`Reserve` to
wait instead of failing, or use `refid/store/postgres` for real concurrent-safe access. This
makes SQLite a convenient choice for local development and tests.

### Bring your own backend

Any type implementing `refid.SequenceStore` or `refid.RandomStore` works — a Redis-backed counter, an in-memory store for tests, etc. Whether `Next`/`Reserve` is safe under concurrent or multi-process use is entirely up to your implementation; the two bundled backends above sit at different points on that spectrum, so check their docs for what each one actually guarantees.

---

## Error Handling

Check sentinel errors using `errors.Is(err, refid.Err...)`:

- `refid.ErrUnknownIssuer` — Issuer not configured in registry.
- `refid.ErrUnknownIDType` — ID Type not found under specified issuer.
- `refid.ErrInvalidParam` — Required param missing, not in allowed list, or scopeKey placeholder un-substituted.
- `refid.ErrCounterOverflow` — Sequence counter value exceeds configured `padding` width.
- `refid.ErrRandomExhausted` — Random segment found no unreserved value within `maxAttempts`; widen the charset/length or narrow the scope key.
- `refid.ErrRandomCollision` — Returned by a `RandomStore.Reserve` implementation when a value is already reserved under a scope key; `Generate` retries internally on this, so callers of `Generate` don't normally see it directly.
