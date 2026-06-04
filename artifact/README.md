# `artifact` — versioned, type-safe config registry

The `artifact` package is a bottom-of-stack registry for loading versioned configuration objects (workflow definitions, task templates, render configs, etc.) from any byte source.

It is deliberately **framework-agnostic and domain-agnostic**: it knows nothing about workflows or tasks. Domain packages bridge to it through adapter packages (see `core/artifactadapter/`).

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

Holds registered loaders and a manifest table `(id, kind, version) → (loaderType, path)`. Created once at startup and shared read-only at runtime.

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
reg := artifact.NewRegistry()

// Register loaders (live clients, credentials, base paths)
reg.RegisterLoader("local", local.New("/etc/configs"))
reg.RegisterLoader("s3",    s3.New(s3Client, "my-bucket"))

// Register artifact rows directly in code...
reg.RegisterArtifact("welcome_email", "email_template", "v2", "local", "emails/welcome.v2.json")

// ...or load from a manifest file
cfg, err := artifact.LoadManifestFile("manifest.yaml")
artifact.RegisterFromConfig(reg, cfg)
```

`RegisterFromConfig` validates that every row's loader type is already registered, so misconfiguration is caught at startup rather than at first access.

### 3. Fetch

```go
// Fetch the newest registered version
tmpl, err := artifact.Latest[EmailTemplate](ctx, reg, "welcome_email")

// Fetch a specific pinned version (e.g. to resume a long-running workflow
// that must stay on the version it started with)
tmpl, err := artifact.Get[EmailTemplate](ctx, reg, "welcome_email", "v2")
```

Both calls: resolve loader → fetch bytes → call `Parse` → return typed value.

---

## Manifest files

`LoadManifestFile` accepts JSON or YAML (detected by extension):

```yaml
# manifest.yaml
artifacts:
  - id: welcome_email
    kind: email_template
    version: v2
    loader: local
    path: emails/welcome.v2.json

  - id: import_clearance
    kind: workflow
    version: v10
    loader: s3
    path: workflows/import_clearance.v10.json
```

---

## Built-in loaders

| Package | Constructor | Description |
|---------|-------------|-------------|
| `artifact/loaders/local` | `local.New(root string)` | Reads files from disk relative to `root` |
| `artifact/loaders/s3`    | `s3.New(client, bucket)` | Reads objects from an S3 bucket |

Both wrap their underlying "not found" errors as `artifact.ErrNotFound` so callers can use `errors.Is`.

---

## Test utilities

`artifact/testutil` exports `MemLoader` — an in-memory `Loader` backed by a `map[string][]byte`:

```go
import "github.com/OpenNSW/core/artifact/testutil"

m := testutil.MemLoader{
    "email/welcome.json": []byte(`{"subject": "Welcome!"}`),
}
reg.RegisterLoader("mem", m)
```

---

## Bridging to domain types

Domain packages (e.g. `taskflow`, `workflow`) typically **don't implement `Artifact` and `Parser` directly** on their types — that would bleed registry concerns into domain code. Instead, adapter packages in `core/artifactadapter/` define private `loadable` wrappers that satisfy the interfaces and expose simple `Load(ctx, reg, id)` helpers.

See [`core/artifactadapter/README.md`](../artifactadapter/README.md) for the full recipe.
