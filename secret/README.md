# Secret Package

`github.com/OpenNSW/core/secret` provides `SecretRef`, a small, dependency-free
primitive for secret-bearing configuration values.

## SecretRef

A `SecretRef` is the raw string as written in config: either a literal value or a
reference whose scheme prefix names where the value comes from. It unmarshals from
and marshals to a plain JSON string — only the single prefixed-string form is
supported; there is intentionally no object form.

| Form                   | Meaning                                                                      |
|:-----------------------|:-----------------------------------------------------------------------------|
| `"plain-value"`        | A literal value (the default — backward compatible).                         |
| `"env:NAME"`           | Read from environment variable `NAME`.                                       |
| `"file:/path/to/file"` | Read from a file; trailing whitespace is trimmed.                            |
| `"literal:env:foo"`    | Explicit literal escape hatch, for a literal that begins with a scheme name. |

A value whose prefix is not a known scheme (including one with no colon at all) is
treated as a literal. This lets non-sensitive configuration (URLs, scopes, header
names) live alongside references to credentials that are provided out-of-band, so
the two are no longer fused into one sensitive blob.

## Usage

```go
type Config struct {
    Token secret.SecretRef `json:"token"`
}

// Resolution — the I/O — is a separate, explicit step.
token, err := cfg.Token.Resolve()
```

`Resolve` is the single I/O seam: the only place that reads env/files and the only
place that can fail. A missing env var or an unreadable/empty file is a **loud
error** — a reference never silently resolves to the empty string. Resolve once, at
startup, and use the returned plain value; if a referenced value changes, restart
the process to pick it up.

## Adding a source

To add a new source (e.g. `vault:`), register a resolver in the `secretSchemes` map
in `secret.go` — no other code changes. File-sourced secrets are capped at 4 KB and
must be regular files.
