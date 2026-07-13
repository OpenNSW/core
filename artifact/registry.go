// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

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

// entry is one manifest row's payload: the path handed to the loader. path is
// opaque to the registry — only the loader interprets it.
type entry struct {
	path string
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

// Registry resolves artifacts by identity. It is backed by a single loader — the
// one source of truth for this deployment (local disk, one bucket, one repo) —
// plus the manifest (grouped so all versions of an id are enumerable for Latest)
// and the version comparator used by Latest. Created once at startup and shared
// read-only at runtime.
type Registry struct {
	loader   Loader                      // the single source, injected at construction
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

// NewRegistry creates a registry backed by a single loader. The loader is the
// source every artifact — and the manifest itself — is fetched from; it is
// required, because a registry with no source can never resolve anything. Panic
// on a nil loader (a startup wiring bug, not runtime input).
func NewRegistry(loader Loader, opts ...Option) *Registry {
	if loader == nil {
		panic("artifact: nil loader passed to NewRegistry")
	}
	r := &Registry{
		loader:   loader,
		manifest: make(map[idKind]map[string]entry),
		less:     defaultVersionLess,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RegisterArtifact adds one manifest row: the path the loader resolves for this
// (id, kind, version). version may be "" for unversioned artifacts. Multiple
// versions of the same (id, kind) accumulate.
func (r *Registry) RegisterArtifact(id string, kind Kind, version, path string) {
	ik := idKind{ID: id, Kind: kind}
	versions, ok := r.manifest[ik]
	if !ok {
		versions = make(map[string]entry)
		r.manifest[ik] = versions
	}
	versions[version] = entry{path: path}
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

// loadAndParse fetches bytes through the registry's loader and parses into T.
// Shared by Get and Latest.
func loadAndParse[T Artifact](ctx context.Context, r *Registry, e entry, id string, kind Kind, version string) (T, error) {
	var zero T
	raw, err := r.loader.Load(ctx, e.path)
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
