# configyaml Package

`github.com/OpenNSW/core/configyaml` loads a YAML config file into a Go
struct, resolving `"{{env:NAME}}"` / `"{{file:/path}}"` placeholders via
[`github.com/OpenNSW/core/secret`](../secret) so secrets never need to live
in the checked-in file.

## Why

Several SDK config types now carry `yaml` struct tags for generic embedding
(`database.Config`, `temporal.Config`, `cors.Config`, and more per-package
going forward). This package is the loader that pairs with them: it decodes
into whatever struct you pass — including one that embeds those tagged
types — without that struct needing to know which of its own fields might be
sourced from an env var or a mounted file. That's a property of how the YAML
file was authored, not of the Go type reading it.

## Usage

```go
type Config struct {
    DB   database.Config `yaml:"db"`
    CORS cors.Config     `yaml:"cors"`
}

var cfg Config
if err := configyaml.LoadAndExpand("config.yaml", &cfg); err != nil {
    log.Fatal(err)
}
```

```yaml
db:
  host: localhost
  port: 5432
  username: postgres
  password: "{{env:DB_PASSWORD}}" # resolved from the DB_PASSWORD env var
cors:
  allowedOrigins: ["https://portal.example.com"]
```

A placeholder must be the *entire* scalar value — `"prefix-{{env:X}}-suffix"`
is left as a literal, not partially resolved. Resolution also isn't limited
to string fields: a non-string field (e.g. a port number) templated the same
way still decodes to the right type.

## Failure mode

Like `secret.SecretRef.Resolve`, a placeholder never silently resolves to the
empty string — an unset env var or missing file is a loud error, reported
with the dotted/indexed YAML path to the offending field (e.g.
`db.password: environment variable "DB_PASSWORD" is not set or is empty`).

## Adding a placeholder scheme

`configyaml` itself doesn't know about schemes — every `{{scheme:ref}}` is
resolved via `secret.SecretRef(ref).Resolve()`. To add a new scheme (e.g.
`vault:`), register it in `secret`'s `secretSchemes` map — see that package's
README.
