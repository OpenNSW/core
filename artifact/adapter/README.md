# Artifact Adapter Layer (`core/artifact/adapter`)

This directory contains adapters that bridge pure domain configuration structures and the bottom-of-stack versioned `artifact` registry.

## The Principle

To keep domain packages (like `workflow` or `taskflow`) and the `artifact` registry cleanly layered:
1. Domain packages may hold an `*artifact.Registry` reference but do not define their own loadable types — they call adapter helpers to fetch configs.
2. The `artifact` package does not import any domain packages.
3. Adapters live in dedicated sub-packages under `core/artifact/adapter/` (for core domain types) or in country repositories, and are the only layer allowed to import both a domain package and `artifact`.

---

## Recipe: Making ANY Type Loadable as an Artifact

To make a type `T` (from package `dom`) loadable via the artifact registry, create a new adapter package (e.g. `core/artifact/adapter/mytype/mytype.go`) that imports both `dom` and `artifact`:

1. **Define the Kind**:
   ```go
   const Kind artifact.Kind = "my_custom_kind"
   ```
2. **Define the Loadable Adapter Struct** (unexported):
   ```go
   type loadable struct {
       dom.T
   }
   ```
3. **Implement `artifact.Artifact`**:
   ```go
   func (loadable) Kind() artifact.Kind { return Kind }
   ```
4. **Implement `artifact.Parser`**:
   ```go
   func (l *loadable) Parse(raw []byte) error {
       // Perform parsing and structural validation of raw bytes into l.T
       val, err := dom.ParseMyType(raw)
       if err != nil {
           return err
       }
       l.T = val
       return nil
   }
   ```
5. **Expose Fetch Helpers**:
   ```go
   // reg is one manifest version's registry: set.Registry(ctx, version).
   func Load(ctx context.Context, reg *artifact.Registry, id string) (dom.T, error) {
       w, err := artifact.Latest[loadable](ctx, reg, id)
       return w.T, err
   }
   ```
6. **Expose a Check** for `artifact.Set.Validate`: parse the bytes and return the IDs the artifact points at, so a manifest that lists it without them is rejected before any flow reaches it:
   ```go
   func Check(raw []byte) ([]artifact.Ref, error) {
       var l loadable
       if err := l.Parse(raw); err != nil {
           return nil, err
       }
       return []artifact.Ref{{ID: l.T.TemplateID, Kinds: []artifact.Kind{"generic_template"}, Field: "template_id"}}, nil
   }
   ```
   Core's own checks are collected in `corechecks.Checks()`; a host adds its kinds to that map.

By following this pattern:
- The registry remains 100% type-agnostic.
- Domain packages remain framework-agnostic.
- Consumers of the domain structures only interact with `dom.T`, keeping the adapter layer transparent.
