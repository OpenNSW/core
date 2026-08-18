// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

# Reference ID Generation System (`refid`)

`refid` is a shared Go package in the OpenNSW system that generates structured, sequential reference IDs based on YAML configuration. It allows any NSW service to define and issue custom reference IDs without writing custom ID generation code.

## Features

- **Config-Driven**: Define ID structures for multiple Issuers and ID Types purely via YAML.
- **Typed Segments**: Concatenate `literal`, `list`, `date`, and `sequence` segments into custom ID formats.
- **Durable Counters**: PostgreSQL-backed atomic sequence increment (`INSERT ... ON CONFLICT DO UPDATE ... RETURNING`).
- **Flexible Resets**: Scope key templates allow counters to reset daily (`{yyyyMMdd}`), monthly (`{yyyyMM}`), yearly (`{yyyy}`), or never.
- **Fail-Fast & Side-Effect Free**: Two-pass generation validates all caller parameters before executing database side-effects.

---

## Quickstart

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/OpenNSW/core/database"
	"github.com/OpenNSW/core/refid"
)

func main() {
	ctx := context.Background()

	// 1. Load configuration
	cfg, err := refid.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// 2. Connect DB and auto-migrate sequence table
	db, err := database.New(dbConfig)
	if err != nil {
		log.Fatalf("failed to connect db: %v", err)
	}
	if err := refid.AutoMigrate(db); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	// 3. Initialize Registry
	store := refid.NewPostgresStore(db)
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

The default `SequenceStore` uses a single PostgreSQL table (`refid_sequences`) with row-level atomic upsert:

```sql
CREATE TABLE IF NOT EXISTS refid_sequences (
    scope_key  TEXT        NOT NULL PRIMARY KEY,
    counter    BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

You can initialize the table automatically via `refid.AutoMigrate(db)`.

---

## Error Handling

Check sentinel errors using `errors.Is(err, refid.Err...)`:

- `refid.ErrUnknownIssuer` — Issuer not configured in registry.
- `refid.ErrUnknownIDType` — ID Type not found under specified issuer.
- `refid.ErrInvalidParam` — Required param missing, not in allowed list, or scopeKey placeholder un-substituted.
- `refid.ErrCounterOverflow` — Sequence counter value exceeds configured `padding` width.
