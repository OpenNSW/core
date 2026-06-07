package artifact

import (
	"context"
	"fmt"
)

// Key is an artifact's full identity: id + kind + version.
// Version is "" for unversioned artifacts (e.g. templates registered once).
type Key struct {
	ID      string
	Kind    Kind
	Version string
}

// entry is one manifest row's payload: which loader fetches it, and the path to
// hand that loader. path is opaque to the registry — only the loader interprets it.
type entry struct {
	loaderType string
	path       string
}

// idKind is the index key under which all versions of one artifact are grouped.
type idKind struct {
	ID   string
	Kind Kind
}

// Loader fetches raw bytes for a path from one source. It knows nothing about
// ids, kinds, versions, or artifact shapes. Return ErrNotFound when the artifact
// is absent; return other errors only for real failures.
type Loader interface {
	Load(ctx context.Context, path string) ([]byte, error)
}

// Registry holds loaders, the manifest (grouped so all versions of an id are
// enumerable for Latest), and the version comparator used by Latest.
type Registry struct {
	loaders  map[string]Loader           // loaderType -> how to fetch
	manifest map[idKind]map[string]entry // (id,kind)  -> version -> entry
	less     func(a, b string) bool      // version "less than" (see version.go)
}

// Option configures a Registry.
type Option func(*Registry)

// WithVersionComparator overrides how Latest picks the newest version. Default is
// defaultVersionLess (numeric-aware; see version.go).
func WithVersionComparator(less func(a, b string) bool) Option {
	return func(r *Registry) {
		r.less = less
	}
}

// NewRegistry creates an empty registry.
func NewRegistry(opts ...Option) *Registry {
	r := &Registry{
		loaders:  make(map[string]Loader),
		manifest: make(map[idKind]map[string]entry),
		less:     defaultVersionLess,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RegisterLoader registers a loader under a type name. Panic on duplicate type
// (a startup wiring bug, not runtime input).
func (r *Registry) RegisterLoader(loaderType string, l Loader) {
	if l == nil {
		panic(fmt.Sprintf("nil loader registered for type: %s", loaderType))
	}
	if _, ok := r.loaders[loaderType]; ok {
		panic(fmt.Sprintf("loader type already registered: %s", loaderType))
	}
	r.loaders[loaderType] = l
}

// RegisterArtifact adds one manifest row. version may be "" for unversioned
// artifacts. Multiple versions of the same (id, kind) accumulate.
func (r *Registry) RegisterArtifact(id string, kind Kind, version, loaderType, path string) {
	ik := idKind{ID: id, Kind: kind}
	versions, ok := r.manifest[ik]
	if !ok {
		versions = make(map[string]entry)
		r.manifest[ik] = versions
	}
	versions[version] = entry{
		loaderType: loaderType,
		path:       path,
	}
}

// Get fetches and parses a specific version of an artifact, as type T. T drives
// BOTH which artifact is fetched (via its Kind) AND how bytes are validated, so a
// configuration mismatch returns an error — it never panics.
//
// Call with VALUE types: Get[EmailTemplate](...), never Get[*EmailTemplate](...).
func Get[T Artifact](ctx context.Context, r *Registry, id, version string) (T, error) {
	var zero T
	kind := kindOf[T]()

	versions, ok := r.manifest[idKind{ID: id, Kind: kind}]
	if !ok {
		return zero, fmt.Errorf("%w: %s/%s", ErrNotFound, id, kind)
	}
	e, ok := versions[version]
	if !ok {
		return zero, fmt.Errorf("%w: %s/%s version %q", ErrNotFound, id, kind, version)
	}
	return loadAndParse[T](ctx, r, e, id, kind, version)
}

// Latest fetches and parses the newest version of an artifact, as type T. For an
// unversioned artifact (a single "" entry) it simply returns that entry, so it is
// also the natural "give me the current one" call for templates and schemas.
func Latest[T Artifact](ctx context.Context, r *Registry, id string) (T, error) {
	var zero T
	kind := kindOf[T]()

	versions, ok := r.manifest[idKind{ID: id, Kind: kind}]
	if !ok || len(versions) == 0 {
		return zero, fmt.Errorf("%w: %s/%s", ErrNotFound, id, kind)
	}

	best, first := "", true
	for v := range versions {
		if first || r.less(best, v) { // r.less(best, v) == "best < v" -> v is newer
			best, first = v, false
		}
	}
	return loadAndParse[T](ctx, r, versions[best], id, kind, best)
}

// loadAndParse resolves the loader, fetches bytes, and parses into T. Shared by
// Get and Latest.
func loadAndParse[T Artifact](ctx context.Context, r *Registry, e entry, id string, kind Kind, version string) (T, error) {
	var zero T
	loader, ok := r.loaders[e.loaderType]
	if !ok {
		return zero, fmt.Errorf("artifact %s/%s/%s wants loader %q, not registered",
			id, kind, version, e.loaderType)
	}
	raw, err := loader.Load(ctx, e.path)
	if err != nil {
		// Includes loader ErrNotFound (surfaced; callers can errors.Is it) and
		// real IO failures.
		return zero, fmt.Errorf("load %s/%s/%s: %w", id, kind, version, err)
	}
	return parseAs[T](raw)
}

// kindOf reads an artifact type's Kind from its zero value. Safe ONLY because
// Kind() is a value-receiver method returning a constant. A pointer T would make
// `var zero T` nil and panic here — hence the value-type rule on Get/Latest.
func kindOf[T Artifact]() Kind {
	var zero T
	return zero.Kind()
}

// parseAs converts raw bytes into T by PARSING, never by asserting. *T must
// implement the exported Parser. A wrong-shaped artifact fails validation inside
// Parse and comes back as an error.
func parseAs[T Artifact](raw []byte) (T, error) {
	var t T
	p, ok := any(&t).(Parser)
	if !ok {
		return t, fmt.Errorf("artifact type %T has no Parse method", t)
	}
	if err := p.Parse(raw); err != nil {
		return t, fmt.Errorf("parse artifact: %w", err)
	}
	return t, nil
}
