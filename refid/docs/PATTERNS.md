# Reference ID Pattern Catalog (`refid`)

`refid` has no notion of a "pattern" baked into the engine — a format is an ordered
list of typed segments that are rendered and concatenated at generation time
(`compileFormat` in [../registry.go](../registry.go)). This document catalogs the ID
shapes that vocabulary can produce, and gives a runnable config for each.

---

## 1. Segment vocabulary

Five segment types, each with its own config fields ([../config.go](../config.go)):

| Type | YAML fields | Renders |
|---|---|---|
| `literal` | `value` | the fixed string, unchanged |
| `list` | `list`, `param` | a caller-supplied param, after validating it against a named list |
| `date` | `layout` | the current UTC time in a Go reference-date layout |
| `sequence` | nested `sequence:` → `scopeKey`, `padding` | a durable counter, zero-padded |
| `random` | nested `random:` → `scopeKey`, `charset`, `length`, `maxAttempts` | a fixed-length random value, checked for collisions |

`sequence` and `random` take their settings in a **nested block** under the segment,
not as flat fields:

```yaml
- type: sequence
  sequence:
    scopeKey: "{issuer}:{idType}:{yyyyMMdd}"
    padding: 6
```

Field bounds worth knowing:

- `padding` — 1 to 18. Exceeding the width returns `ErrCounterOverflow`.
- `charset` — one of `numeric` (`0-9`), `alpha` (`A-Z`), `alphanumeric` (`A-Z0-9`).
- `length` — at least 1; no upper bound.
- `maxAttempts` — optional, 0 to 100. **`0` means "use the default" (10), not "no attempts".**

---

## 2. Composition rules

What the engine actually enforces:

- A format needs **at least one** segment.
- A format may contain **at most one stateful segment** — `sequence` and `random` are
  stateful (they commit to a store); `literal`, `list`, and `date` are pure functions of
  the caller's params and the current time. See
  [Stateful Segment Limit](../README.md#stateful-segment-limit) for why.
- Everything else is free: `literal`, `list`, and `date` may appear any number of times,
  in any order, before or after the stateful segment.

So a format is: any arrangement of pure segments, with at most one counter or random
value dropped in anywhere.

---

## 3. Scope keys and reset cadence

Both stateful types resolve a `scopeKey` template per call (`resolveScopeKey` in
[../segments.go](../segments.go)). For `sequence` the resolved key selects which counter
to increment; for `random` it selects which set of already-issued values the new value
must not collide with. Either way, each distinct resolved key is an independent space.

Reserved placeholders: `{issuer}`, `{idType}`, `{yyyy}`, `{yyyyMM}`, `{yyyyMMdd}`, and
`{<param>}` for any caller-supplied param (reserved tokens win over a param of the same name).

| Cadence | `scopeKey` | Counter resets / uniqueness set clears |
|---|---|---|
| Never | `{issuer}:{idType}` | Never — one space forever |
| Never, per param | `{issuer}:{idType}:{officeCode}` | Never, but each param value gets its own space |
| Yearly | `{issuer}:{idType}:{yyyy}` | Jan 1 UTC |
| Monthly | `{issuer}:{idType}:{yyyyMM}` | 1st of month UTC |
| Daily | `{issuer}:{idType}:{yyyyMMdd}` | Midnight UTC |
| Daily, per param | `{issuer}:{idType}:{officeCode}:{yyyyMMdd}` | Midnight UTC, independently per param value |
| Multi-param | `{issuer}:{idType}:{officeCode}:{deptCode}` | Never, independently per (office, dept) pair |

There is no quarterly or weekly placeholder. A cadence outside the three date tokens needs
a caller-supplied param (e.g. pass `quarter: "2026Q3"` and reference `{quarter}`), since Go's
time layouts cannot format quarters.

---

## 4. Random keyspace

Values are drawn from `crypto/rand`, one uniform draw per character. The charset and length
together fix how many distinct values a scope can ever hold:

| `charset` | `length: 4` | `length: 6` | `length: 8` |
|---|---|---|---|
| `numeric` (10 chars) | 10,000 | 1,000,000 | 100,000,000 |
| `alpha` (26 chars) | 456,976 | 308,915,776 | 208,827,064,576 |
| `alphanumeric` (36 chars) | 1,679,616 | 2,176,782,336 | 2,821,109,907,456 |

As a scope fills, collisions get likelier; each one costs a retry, and running out of
retries returns `ErrRandomExhausted`. Size `length` so the scope stays a small fraction of
its keyspace — and remember a dated `scopeKey` shrinks the demand on each space rather than
the space itself.

> [!NOTE]
> The alphabets are uppercase-only and do **not** exclude confusable glyphs — `0`/`O` and
> `1`/`I` both appear in `alphanumeric`. Consider that before printing values on something a
> human reads back.

---

## 5. Catalog of ID shapes

Sequence examples show a representative counter value (a fresh scope starts at 1); random
examples show one possible draw. Each row has a runnable config in the appendix.

**Sequence-based** — monotonic, gapless within a scope:

| # | Segments | Example | Scope |
|---|---|---|---|
| S1 | `sequence` | `000042` | Never resets |
| S2 | `literal` + `sequence` | `INV-00000042` | Never resets |
| S3 | `literal` + `list` + `sequence` | `RTA-PMT-COL-00000001` | Per office, never resets |
| S4 | `literal` + `list` + `date` + `sequence` | `RTA-APP-COL-20260817-000042` | Per office, daily |
| S5 | `literal` + `date` + `sequence` | `CASE-2026-00001` | Yearly |
| S6 | `literal` + `list` ×2 + `sequence` | `HR-COL-PAY-000012` | Per (office, dept) |
| S7 | `date` + `literal` + `sequence` | `20260817-RTA-000042` | Daily |

**Random-based** — unguessable, no ordering, uniqueness enforced per scope:

| # | Segments | Example | Scope |
|---|---|---|---|
| R1 | `literal` + `random` | `RTA-VCH-7K2QQXAB` | Unique per idType |
| R2 | `random` | `7K2QQXAB` | Unique per idType |
| R3 | `literal` + `list` + `random` | `RTA-COL-KDQZFM` | Unique per office |
| R4 | `literal` + `date` + `random` | `TKT-20260817-9042` | Unique per day |

**No stateful segment** — pure functions of params and the clock:

| # | Segments | Example | Note |
|---|---|---|---|
| C1 | `literal` | `STATIC-CODE` | Constant; every call returns the same string |
| C2 | `literal` + `list` + `date` | `RTA-COL-20260817` | Deterministic; repeats for the same params on the same day |

---

## Appendix: Example configs

Each config below is a complete `refid.Config`. Load and use one with:

```go
cfg, err := refid.LoadConfig("config.yaml")
reg, err := refid.NewRegistry(cfg,
    refid.WithSequenceStore(seqStore),  // needed for sequence segments
    refid.WithRandomStore(randStore),   // needed for random segments
)
```

A store option is only required if the config actually uses that segment type — C1 and C2
need neither.

### S1 — `sequence`

```yaml
issuers:
  - issuer: ACME
    formats:
      - idType: ticket_id
        segments:
          - type: sequence
            sequence:
              scopeKey: "{issuer}:{idType}"
              padding: 6
```

`Generate(ctx, "ACME", "ticket_id", nil)` → `000042`

### S2 — `literal` + `sequence`

```yaml
issuers:
  - issuer: ACME
    formats:
      - idType: invoice_id
        segments:
          - type: literal
            value: "INV-"
          - type: sequence
            sequence:
              scopeKey: "{issuer}:{idType}"
              padding: 8
```

`Generate(ctx, "ACME", "invoice_id", nil)` → `INV-00000042`

### S3 — `literal` + `list` + `sequence`, per-office counter

```yaml
issuers:
  - issuer: RTA
    formats:
      - idType: permit_id
        segments:
          - type: literal
            value: "RTA-PMT-"
          - type: list
            list: office_location
            param: officeCode
          - type: literal
            value: "-"
          - type: sequence
            sequence:
              scopeKey: "{issuer}:{idType}:{officeCode}"
              padding: 8

lists:
  office_location: [COL, GAL, KAN]
```

`Generate(ctx, "RTA", "permit_id", map[string]string{"officeCode": "COL"})` → `RTA-PMT-COL-00000001`

### S4 — `literal` + `list` + `date` + `sequence`, daily reset per office

```yaml
issuers:
  - issuer: RTA
    formats:
      - idType: application_id
        segments:
          - type: literal
            value: "RTA-APP-"
          - type: list
            list: office_location
            param: officeCode
          - type: literal
            value: "-"
          - type: date
            layout: "20060102"
          - type: literal
            value: "-"
          - type: sequence
            sequence:
              scopeKey: "{issuer}:{idType}:{officeCode}:{yyyyMMdd}"
              padding: 6

lists:
  office_location: [COL, GAL, KAN]
```

`Generate(ctx, "RTA", "application_id", map[string]string{"officeCode": "COL"})` → `RTA-APP-COL-20260817-000042`

### S5 — `literal` + `date` + `sequence`, yearly reset

```yaml
issuers:
  - issuer: FCAU
    formats:
      - idType: case_id
        segments:
          - type: literal
            value: "CASE-"
          - type: date
            layout: "2006"
          - type: literal
            value: "-"
          - type: sequence
            sequence:
              scopeKey: "{issuer}:{idType}:{yyyy}"
              padding: 5
```

`Generate(ctx, "FCAU", "case_id", nil)` → `CASE-2026-00001`

### S6 — `literal` + `list` ×2 + `sequence`, counter per (office, department)

```yaml
issuers:
  - issuer: HR
    formats:
      - idType: payroll_id
        segments:
          - type: literal
            value: "HR-"
          - type: list
            list: office_location
            param: officeCode
          - type: literal
            value: "-"
          - type: list
            list: department
            param: deptCode
          - type: literal
            value: "-"
          - type: sequence
            sequence:
              scopeKey: "{issuer}:{idType}:{officeCode}:{deptCode}"
              padding: 6

lists:
  office_location: [COL, GAL, KAN]
  department: [PAY, HRM, LEG]
```

`Generate(ctx, "HR", "payroll_id", map[string]string{"officeCode": "COL", "deptCode": "PAY"})` → `HR-COL-PAY-000012`

### S7 — `date` + `literal` + `sequence`, date-first ordering

Segment order is entirely caller-defined; nothing requires a literal prefix to come first.

```yaml
issuers:
  - issuer: RTA
    formats:
      - idType: daily_log_id
        segments:
          - type: date
            layout: "20060102"
          - type: literal
            value: "-RTA-"
          - type: sequence
            sequence:
              scopeKey: "{issuer}:{idType}:{yyyyMMdd}"
              padding: 6
```

`Generate(ctx, "RTA", "daily_log_id", nil)` → `20260817-RTA-000042`

### R1 — `literal` + `random`

```yaml
issuers:
  - issuer: RTA
    formats:
      - idType: voucher_id
        segments:
          - type: literal
            value: "RTA-VCH-"
          - type: random
            random:
              scopeKey: "{issuer}:{idType}"
              charset: alphanumeric
              length: 8
              maxAttempts: 10
```

`Generate(ctx, "RTA", "voucher_id", nil)` → `RTA-VCH-7K2QQXAB`

### R2 — `random`

```yaml
issuers:
  - issuer: ACME
    formats:
      - idType: access_code
        segments:
          - type: random
            random:
              scopeKey: "{issuer}:{idType}"
              charset: alphanumeric
              length: 8
```

`Generate(ctx, "ACME", "access_code", nil)` → `7K2QQXAB`

### R3 — `literal` + `list` + `random`, uniqueness scoped per office

```yaml
issuers:
  - issuer: RTA
    formats:
      - idType: claim_id
        segments:
          - type: literal
            value: "RTA-"
          - type: list
            list: office_location
            param: officeCode
          - type: literal
            value: "-"
          - type: random
            random:
              scopeKey: "{issuer}:{idType}:{officeCode}"
              charset: alpha
              length: 6

lists:
  office_location: [COL, GAL, KAN]
```

`Generate(ctx, "RTA", "claim_id", map[string]string{"officeCode": "COL"})` → `RTA-COL-KDQZFM`

### R4 — `literal` + `date` + `random`, uniqueness scoped per day

A dated `scopeKey` keeps each day's uniqueness set small, which is what makes a 4-digit
numeric value (10,000 possibilities) workable here.

```yaml
issuers:
  - issuer: RTA
    formats:
      - idType: token_id
        segments:
          - type: literal
            value: "TKT-"
          - type: date
            layout: "20060102"
          - type: literal
            value: "-"
          - type: random
            random:
              scopeKey: "{issuer}:{idType}:{yyyyMMdd}"
              charset: numeric
              length: 4
```

`Generate(ctx, "RTA", "token_id", nil)` → `TKT-20260817-9042`

### C1 — `literal`, constant ID

Legal — a format only needs one segment — but every call returns the identical string.

```yaml
issuers:
  - issuer: ACME
    formats:
      - idType: static_code
        segments:
          - type: literal
            value: "STATIC-CODE"
```

`Generate(ctx, "ACME", "static_code", nil)` → `STATIC-CODE`

### C2 — `literal` + `list` + `date`, deterministic key

No stateful segment, so no store is needed and no uniqueness is implied: the same params on
the same day produce the same string every time. Useful as a grouping or batch key rather
than an identifier.

```yaml
issuers:
  - issuer: RTA
    formats:
      - idType: batch_key
        segments:
          - type: literal
            value: "RTA-"
          - type: list
            list: office_location
            param: officeCode
          - type: literal
            value: "-"
          - type: date
            layout: "20060102"

lists:
  office_location: [COL, GAL, KAN]
```

`Generate(ctx, "RTA", "batch_key", map[string]string{"officeCode": "COL"})` → `RTA-COL-20260817`
