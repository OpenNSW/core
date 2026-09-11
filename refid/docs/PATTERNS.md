# Reference ID Pattern Catalog (`refid`)

`refid` has no notion of a "pattern" baked into the engine — a format is just an
ordered list of segments (`literal`, `list`, `date`, `sequence`) that get
concatenated at generation time (see [../registry.go](../registry.go),
[../segments.go](../segments.go)). This document catalogs the pattern space
that falls out of that grammar, and gives a runnable config for each shape.

---

## 1. The pattern grammar

```
format   := segment+
segment  := literal | list | date | sequence

literal  := any fixed string                                  → "RTA-APP-"
list     := one caller param, validated against a named list  → "COL"
date     := now.Format(any Go reference layout)               → "20260817"
sequence := durable counter, scoped by a template,
            1-18 digit zero-pad                               → "000042"
```

Segments may appear zero or more times, in any order — nothing in
`compileFormat` ([../registry.go:173-187](../registry.go#L173-L187)) constrains
count or ordering beyond "at least one segment total."

Because `literal` text, `date` layouts, and `scopeKey` templates are
free-form strings, the pattern space is technically infinite. What follows
are the two finite dimensions that actually change generated-ID *behavior*
(§2), plus a representative catalog of shapes (§3) with a config per shape
in the appendix.

---

## 2. Sequence reset cadence

Driven entirely by which placeholders appear in a sequence segment's
`scopeKey` (`resolveScopeKey`, [../segments.go:194-222](../segments.go#L194-L222)):

| Cadence | `scopeKey` pattern | Resets when |
|---|---|---|
| Never | `{issuer}:{idType}` | Never — one running counter forever |
| Never, per param | `{issuer}:{idType}:{officeCode}` | Never, but each param value gets its own counter |
| Yearly | `{issuer}:{idType}:{yyyy}` | Jan 1 UTC |
| Monthly | `{issuer}:{idType}:{yyyyMM}` | 1st of month UTC |
| Daily | `{issuer}:{idType}:{yyyyMMdd}` | Midnight UTC |
| Daily, per param | `{issuer}:{idType}:{officeCode}:{yyyyMMdd}` | Midnight UTC, independently per param value |
| Multi-param | `{issuer}:{idType}:{officeCode}:{deptCode}` | Never, independently per (office, dept) pair |

There is no built-in quarterly or weekly placeholder — only `{yyyy}`,
`{yyyyMM}`, `{yyyyMMdd}` are reserved. A quarterly-style reset would need a
caller-supplied param (e.g. `params["quarter"] = "2026Q3"`, referenced as
`{quarter}` in the `scopeKey`), since Go's time layout cannot format
quarters directly.

---

## 3. Catalog of ID shapes

| # | Segments used | Example shape | Reset cadence |
|---|---|---|---|
| P1 | `sequence` only | `000042` | Never |
| P2 | `literal` + `sequence` | `INV-00000042` | Never |
| P3 | `literal` + `list` + `sequence` | `RTA-PMT-COL-00000001` | Never, per office |
| P4 | `literal` + `list` + `date` + `sequence` | `RTA-APP-COL-20260817-000042` | Daily, per office |
| P5 | `literal` + `date` + `sequence` (no list) | `CASE-2026-00001` | Yearly |
| P6 | `literal` + `list` ×2 + `sequence` | `HR-COL-PAY-000012` | Never, per (office, dept) |
| P7 | `date` + `literal` + `sequence` (reversed order) | `20260817-RTA-000042` | Daily |
| P8 | `literal` + `sequence` ×2 | `A-000042-000007` | Never, two independent counters |
| P9 | `literal` only (no dynamic segments) | `STATIC-CODE` | N/A — constant |

Each row is a fully runnable `refid.Config` in the appendix below.

---

## Appendix: Example configs per shape

### P1 — `sequence` only

```yaml
issuers:
  - issuer: ACME
    formats:
      - idType: ticket_id
        segments:
          - type: sequence
            scopeKey: "{issuer}:{idType}"
            padding: 6
```

`Generate(ctx, "ACME", "ticket_id", nil)` → `000042`

### P2 — `literal` + `sequence`, never resets

```yaml
issuers:
  - issuer: ACME
    formats:
      - idType: invoice_id
        segments:
          - type: literal
            value: "INV-"
          - type: sequence
            scopeKey: "{issuer}:{idType}"
            padding: 8
```

`Generate(ctx, "ACME", "invoice_id", nil)` → `INV-00000042`

### P3 — `literal` + `list` + `sequence`, per-office counter that never resets

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
            scopeKey: "{issuer}:{idType}:{officeCode}"
            padding: 8

lists:
  office_location: [COL, GAL, KAN]
```

`Generate(ctx, "RTA", "permit_id", map[string]string{"officeCode": "COL"})` → `RTA-PMT-COL-00000001`

### P4 — `literal` + `list` + `date` + `sequence`, daily reset per office

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
            scopeKey: "{issuer}:{idType}:{officeCode}:{yyyyMMdd}"
            padding: 6

lists:
  office_location: [COL, GAL, KAN]
```

`Generate(ctx, "RTA", "application_id", map[string]string{"officeCode": "COL"})` → `RTA-APP-COL-20260817-000042`

### P5 — `literal` + `date` + `sequence`, yearly reset, no list

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
            scopeKey: "{issuer}:{idType}:{yyyy}"
            padding: 5
```

`Generate(ctx, "FCAU", "case_id", nil)` → `CASE-2026-00001`

### P6 — `literal` + `list` ×2 + `sequence`, counter per (office, department)

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
            scopeKey: "{issuer}:{idType}:{officeCode}:{deptCode}"
            padding: 6

lists:
  office_location: [COL, GAL, KAN]
  department: [PAY, HRM, LEG]
```

`Generate(ctx, "HR", "payroll_id", map[string]string{"officeCode": "COL", "deptCode": "PAY"})` → `HR-COL-PAY-000012`

### P7 — `date` + `literal` + `sequence`, segments in non-conventional order

Segment order is caller-defined; nothing requires the ID to start with a
literal prefix.

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
            scopeKey: "{issuer}:{idType}:{yyyyMMdd}"
            padding: 6
```

`Generate(ctx, "RTA", "daily_log_id", nil)` → `20260817-RTA-000042`

### P8 — `literal` + `sequence` ×2, two independent counters in one ID

Each `sequence` segment needs its own distinct `scopeKey` (here via a
literal suffix baked into the template) — otherwise both segments would
resolve to the same scope key and share one counter instead of two.

```yaml
issuers:
  - issuer: ACME
    formats:
      - idType: dual_seq_id
        segments:
          - type: literal
            value: "A-"
          - type: sequence
            scopeKey: "{issuer}:{idType}:primary"
            padding: 6
          - type: literal
            value: "-"
          - type: sequence
            scopeKey: "{issuer}:{idType}:secondary"
            padding: 6
```

`Generate(ctx, "ACME", "dual_seq_id", nil)` → `A-000042-000007`

### P9 — `literal` only, constant ID

Legal, since `compileFormat` only requires at least one segment — but every
call to `Generate` for this `idType` returns the exact same string.

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
