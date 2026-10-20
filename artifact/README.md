# `artifact` — versioned, type-safe config registry

The `artifact` package is a bottom-of-stack registry for loading versioned configuration objects (workflow definitions, task templates, render configs, etc.) from any byte source.

It is deliberately **framework-agnostic and domain-agnostic**: it knows nothing about workflows or tasks. Domain packages bridge to it through adapter packages (see `core/artifact/adapter/`).

---

## Core concepts

### `Artifact`

Any type the registry can return. Implemented as a value type with a constant `Kind()` method:

```go
type Artifact interface {
    Kind() Kind
}
```

`Kind` is an open string — any package can define its own without editing core.

### `Parser`

Implemented on `*T`. Populates the value from raw bytes **and validates its shape**, turning a wrong-shaped artifact into a returned error rather than a silent zero value:

```go
type Parser interface {
    Parse(raw []byte) error
}
```

### `Loader`

Fetches raw bytes from one source (disk, S3, memory). Knows nothing about ids, kinds, or versions:

```go
type Loader interface {
    Load(ctx context.Context, path string) ([]byte, error)
}
```

Return `artifact.ErrNotFound` when the resource is absent; return other errors only for genuine I/O failures.

### `Registry`

Resolves `(id, kind, version) → path` for **one manifest** and fetches through the single loader. Shared read-only at runtime.

### `Set`

Holds every published manifest of one source, one `Registry` per manifest version, and names the current one. A flow records `set.Current()` when it starts and resolves every artifact through `set.Registry(ctx, version)` until it ends, so a deploy never changes the templates of a running flow. See [Versioned manifests](#versioned-manifests).

---

## Usage

### 1. Implement `Artifact` and `Parser` on your type

```go
type EmailTemplate struct {
    Subject string `json:"subject"`
}

func (EmailTemplate) Kind() artifact.Kind { return "email_template" }

func (e *EmailTemplate) Parse(raw []byte) error {
    if err := json.Unmarshal(raw, e); err != nil {
        return err
    }
    if e.Subject == "" {
        return fmt.Errorf("email template: missing subject")
    }
    return nil
}
```

`Kind()` must be a **value receiver** returning a constant. `Parse()` must be a **pointer receiver** (it mutates the value). `Get` and `Latest` require value types, not pointers:

```go
// Correct
artifact.Latest[EmailTemplate](ctx, reg, "welcome")

// Wrong — will panic: pointer zero value is nil
artifact.Latest[*EmailTemplate](ctx, reg, "welcome")
```

### 2. Wire up at startup

```go
// One loader is the single source of truth for every manifest and artifact.
loader, err := local.New(local.Config{Root: "/etc/configs"})

// Load manifest_v1.json, manifest_v2.json, … and pick the current version.
set, err := artifact.LoadSet(ctx, loader)                                // highest version
set, err := artifact.LoadSet(ctx, loader, artifact.WithPinnedVersion(4)) // or a pinned one
```

A bare `Registry` can still be built by hand, for tests:

```go
reg := artifact.NewRegistry(loader)
reg.RegisterArtifact("welcome_email", "email_template", "", "emails/welcome.json")
```

The loader is required — `NewRegistry(nil)` panics and `LoadSet(ctx, nil)` fails. `RegisterFromConfig` rejects a row without an `id`, `kind`, or `path`, and two rows with the same `id`, `kind` and `version`.

### 3. Fetch

```go
// Inside a flow: the version the flow recorded when it started.
reg, err := set.Registry(ctx, flow.ManifestVersion)
tmpl, err := artifact.Latest[EmailTemplate](ctx, reg, "welcome_email")

// Outside any flow, for an artifact requested by explicit version (static data):
list, err := artifact.GetExplicit[StaticList](ctx, set, "ports", "2")
```

All calls: fetch bytes via the loader → call `Parse` → return typed value. Within one manifest an artifact without an explicit version has a single row, so `Latest` returns it.

---

## Versioned manifests

Manifests are `manifest_v1.json`, `manifest_v2.json`, … at the loader's root (`artifact.ManifestPath(v)`), alongside the artifacts they catalog. There is no per-row `loader`: the source has exactly one.

```json
{
  "artifacts": [
    { "id": "welcome_email",   "kind": "email_template", "path": "emails/welcome_v2.json" },
    { "id": "import_clearance", "kind": "workflow",       "path": "workflows/import_clearance.json" },
    { "id": "ports",            "kind": "static_data",    "version": "2", "path": "static/ports_v2.json" }
  ]
}
```

The rules that make a manifest version a stable snapshot:

- **Published files never change.** A change to an artifact is a new file (`welcome_v2.json`), and a new manifest, with the next version number, points the same ID at it. Every older manifest keeps resolving what it resolved when it was published.
- **Artifacts refer to each other by ID.** A task template names its workflow and render config by ID, and the manifest decides which file each ID is. Changing a render config needs a new manifest, not a new task template.
- **Versions are contiguous from 1.** `LoadSet` probes upward until the loader reports `ErrNotFound`, so a gap hides every later manifest. Any other load error fails `LoadSet`; for S3 this means the reader needs `s3:ListBucket`, or a missing key is reported as access denied.
- **An explicit `version` keeps its path.** A row with a `version` is fetched by that version outside any flow (`GetExplicit`), so every manifest that lists it must point at the same file. `LoadSet` rejects one that does not.
- **The current version** is the highest one, unless `WithPinnedVersion` names an older one for a rollback. With a pin, `LoadSet` reads only manifests up to it, so a broken newer manifest cannot block the rollback.
- **Newer manifests load on demand.** Replicas load their sets at different times. `Set.Registry` loads a version above the highest it has, so a flow pinned by a newer replica still runs on an older one. It fetches only up to the version asked for, holds no lock that lookups wait on while it fetches, and after a probe that fails or finds nothing it waits 10s before probing again. `Current` does not move until the set is reloaded.
- **Within a manifest, the unversioned row wins.** `Latest` returns the row without a version when there is one, even if the manifest also lists explicit versions of the same ID.

---

## Built-in loaders

| Package                   | Constructor                      | Description                                                                                                                                                                                                                                              |
|---------------------------|----------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `artifact/loaders/local`  | `local.New(local.Config{Root})`  | Reads files from disk relative to `Root`; validates `Root` exists and is a directory                                                                                                                                                                     |
| `artifact/loaders/s3`     | `s3.New(ctx, s3.Config{...})`    | Reads objects from an S3 bucket; builds the client from `Region`/`Endpoint`/credentials (S3-compatible stores supported); `Prefix` scopes an in-bucket sub-folder for multi-deployment buckets                                                           |
| `artifact/loaders/github` | `github.New(github.Config{...})` | Reads files from a GitHub repo at a pinned `Ref` over the Contents API (`net/http`); `BasePath` scopes a sub-directory for multi-deployment repos. Set `UseRawHost` to fetch public repos via `raw.githubusercontent.com` (no token, no REST rate limit) |

All wrap their underlying "not found" errors as `artifact.ErrNotFound` so callers can use `errors.Is`.

### Selecting a loader from config

`artifact/loaders` exposes a `Config`/`New` pair that picks the concrete loader
at runtime via `Config.Type` ("local", "github", or "s3"), reading only the
matching embedded backend config:

```go
import (
    "github.com/OpenNSW/core/artifact/loaders"
    "github.com/OpenNSW/core/artifact/loaders/local"
)

loader, err := loaders.New(ctx, loaders.Config{
    Type: "local",
    Local: local.Config{Root: "/etc/configs"},
})
```

`loaders.Config` and each backend's own `Config` (`local.Config`,
`github.Config`, `s3.Config`) carry `yaml` struct tags, so the whole tree can
be embedded in a larger application config struct and populated generically
(e.g. via `yaml.Unmarshal`) instead of being constructed by hand:

```yaml
artifacts:
  type: s3
  s3:
    bucket: my-bucket
    region: us-east-1
    prefix: deployment-a
```

`github.Config.HTTPClient` has no YAML representation and is excluded from
unmarshaling.

---

## Test utilities

`artifact/testutil` exports `MemLoader` — an in-memory `Loader` backed by a `map[string][]byte`:

```go
import "github.com/OpenNSW/core/artifact/testutil"

m := testutil.MemLoader{
    "email/welcome.json": []byte(`{"subject": "Welcome!"}`),
}
reg := artifact.NewRegistry(m)
```

Put `manifest_v1.json` (and later versions) in a `MemLoader` to test against a `Set`.

---

## Bridging to domain types

Domain packages (e.g. `taskflow`, `workflow`) typically **don't implement `Artifact` and `Parser` directly** on their types — that would bleed registry concerns into domain code. Instead, adapter packages in `core/artifact/adapter/` define private `loadable` wrappers that satisfy the interfaces and expose simple `Load(ctx, reg, id)` helpers.

See [`core/artifact/adapter/README.md`](adapter/README.md) for the full recipe.
