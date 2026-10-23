// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
)

// ErrUnknownManifestVersion is returned when a manifest version is not positive or no manifest
// file exists for it. It does not match ErrNotFound: a flow pinned to a missing manifest is a
// deployment fault, not a missing artifact.
var ErrUnknownManifestVersion = errors.New("unknown manifest version")

// Set holds every published manifest of one artifact source, each as its own Registry, and
// names the current one.
//
// Artifacts are immutable once a manifest lists them. A change is a new file, listed by a new
// manifest with the next version number, so an older manifest keeps resolving exactly what it
// resolved when it was published. A flow records the current version when it starts and
// resolves every artifact through Registry(version) until it ends, so a deploy never changes
// the templates of a flow already running.
//
// Manifests are found by probing manifest_v1.json, manifest_v2.json, … until one is missing,
// so versions must be contiguous. A source with no manifest_v1.json but a manifest.json (a
// checkout of an artifacts repository, which holds only the current state) loads that file as
// version 1. A Set does not change once loaded, so it is safe for concurrent use.
type Set struct {
	loader Loader

	views   map[int]*Registry
	latest  int
	current int

	// explicit records the path each explicitly versioned row resolves to, across every loaded
	// manifest, so a later manifest cannot repoint an (id, kind, version) at other content.
	explicit map[Key]explicitRow
}

type explicitRow struct {
	path     string
	manifest int
}

// SetOption configures LoadSet.
type SetOption func(*setOptions)

type setOptions struct {
	pin int
}

// WithPinnedVersion makes manifest v the current one instead of the highest published version.
// It serves rollback, and lets a manifest be published before new flows use it. Zero means no
// pin. A pin to a version that does not exist fails LoadSet.
//
// With a pin, LoadSet reads only manifests 1 to v, so a newer manifest, even a malformed one,
// cannot stop a rollback to v.
func WithPinnedVersion(v int) SetOption {
	return func(o *setOptions) { o.pin = v }
}

// LoadSet loads every manifest the loader can see, starting at manifest_v1.json, and picks the
// current version. Loading stops at the first version whose manifest the loader reports as
// ErrNotFound, or at the pinned version; any other load error fails the call, so a transient
// fault never silently hides the newer manifests. Without a manifest_v1.json, it loads
// manifest.json as version 1. Each manifest is checked as RegisterFromConfig checks it, and
// explicitly versioned rows are checked for a consistent path across manifests. Artifact files
// themselves are not fetched.
func LoadSet(ctx context.Context, loader Loader, opts ...SetOption) (*Set, error) {
	if loader == nil {
		return nil, errors.New("artifact: nil loader passed to LoadSet")
	}
	var o setOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.pin < 0 {
		return nil, fmt.Errorf("%w: pinned version %d", ErrUnknownManifestVersion, o.pin)
	}

	s := &Set{loader: loader, views: make(map[int]*Registry), explicit: make(map[Key]explicitRow)}
	cfgs, err := fetchManifests(ctx, loader, 1, o.pin)
	if err != nil {
		return nil, err
	}
	if len(cfgs) == 0 && o.pin <= 1 {
		cfg, err := loadUnversioned(ctx, loader)
		if err != nil {
			return nil, err
		}
		if cfg != nil {
			cfgs = append(cfgs, *cfg)
		}
	}
	for i, cfg := range cfgs {
		if err := s.add(i+1, cfg); err != nil {
			return nil, err
		}
	}
	if s.latest == 0 {
		return nil, fmt.Errorf("%w: no %s or %s found", ErrUnknownManifestVersion, ManifestPath(1), ManifestFilename)
	}
	if o.pin > s.latest {
		return nil, fmt.Errorf("%w: pinned version %d, highest published is %d", ErrUnknownManifestVersion, o.pin, s.latest)
	}
	s.current = s.latest
	slog.InfoContext(ctx, "artifact manifests loaded", "latest", s.latest, "current", s.current, "pinned", o.pin != 0)
	return s, nil
}

// fetchManifests fetches manifests from version from upward, stopping at the first the loader
// reports missing or after version to (0: no limit). It returns those fetched before any other
// error, with that error.
func fetchManifests(ctx context.Context, loader Loader, from, to int) ([]ManifestConfig, error) {
	var cfgs []ManifestConfig
	for v := from; to == 0 || v <= to; v++ {
		cfg, err := LoadManifest(ctx, loader, v)
		if errors.Is(err, ErrNotFound) {
			return cfgs, nil
		}
		if err != nil {
			return cfgs, err
		}
		cfgs = append(cfgs, cfg)
	}
	return cfgs, nil
}

// loadUnversioned loads manifest.json, or returns nil if the loader has none.
func loadUnversioned(ctx context.Context, loader Loader) (*ManifestConfig, error) {
	data, err := loader.Load(ctx, ManifestFilename)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load manifest %q: %w", ManifestFilename, err)
	}
	cfg, err := parseManifest(ManifestFilename, data)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// add registers manifest v. It runs only while LoadSet builds s.
func (s *Set) add(v int, cfg ManifestConfig) error {
	reg := NewRegistry(s.loader)
	if err := RegisterFromConfig(reg, cfg); err != nil {
		return fmt.Errorf("%s: %w", ManifestPath(v), err)
	}
	// Check every explicit row before recording any, so a rejected manifest leaves no trace.
	for _, row := range cfg.Artifacts {
		if row.Version == "" {
			continue
		}
		k := Key{ID: row.ID, Kind: row.Kind, Version: row.Version}
		if prev, ok := s.explicit[k]; ok && prev.path != row.Path {
			return fmt.Errorf("%s: %s/%s version %q points at %q, but %s points it at %q; an explicit version must keep its file",
				ManifestPath(v), row.ID, row.Kind, row.Version, row.Path, ManifestPath(prev.manifest), prev.path)
		}
	}
	for _, row := range cfg.Artifacts {
		if row.Version == "" {
			continue
		}
		k := Key{ID: row.ID, Kind: row.Kind, Version: row.Version}
		if _, ok := s.explicit[k]; !ok {
			s.explicit[k] = explicitRow{path: row.Path, manifest: v}
		}
	}
	s.views[v] = reg
	s.latest = v
	return nil
}

// Current returns the version new flows should pin: the highest published at load time, or the
// pinned one. A manifest published later becomes current when the process reloads the set.
func (s *Set) Current() int {
	return s.current
}

// Latest returns the highest manifest version loaded.
func (s *Set) Latest() int {
	return s.latest
}

// Versions returns every loaded manifest version, ascending.
func (s *Set) Versions() []int {
	vs := make([]int, 0, len(s.views))
	for v := range s.views {
		vs = append(vs, v)
	}
	slices.Sort(vs)
	return vs
}

// Registry returns the registry for manifest version v, or ErrUnknownManifestVersion if no
// loaded manifest has that version.
func (s *Set) Registry(_ context.Context, v int) (*Registry, error) {
	reg, ok := s.views[v]
	if !ok {
		return nil, fmt.Errorf("%w: %d (highest published is %d)", ErrUnknownManifestVersion, v, s.latest)
	}
	return reg, nil
}

// CurrentRegistry returns the registry for the current version.
func (s *Set) CurrentRegistry() *Registry {
	return s.views[s.current]
}

// GetExplicit fetches an explicitly versioned artifact without a manifest version, from
// whichever manifest lists it. LoadSet guarantees every manifest that lists an (id, kind,
// version) points it at the same path, so the result does not depend on which one is used.
// Use it for artifacts requested by explicit version outside any flow, such as static data.
func GetExplicit[T Artifact](ctx context.Context, s *Set, id, version string) (T, error) {
	var zero T
	kind := kindOf[T]()
	if version == "" {
		return zero, fmt.Errorf("%w: %s/%s needs an explicit version", ErrNotFound, id, kind)
	}
	key := Key{ID: id, Kind: kind, Version: version}
	reg, ok := s.explicitRegistry(key)
	if !ok {
		return zero, fmt.Errorf("%w: %s/%s version %q", ErrNotFound, id, kind, version)
	}
	return Get[T](ctx, reg, id, version)
}

// explicitRegistry returns a registry of a loaded manifest that lists key.
func (s *Set) explicitRegistry(key Key) (*Registry, bool) {
	row, ok := s.explicit[key]
	if !ok {
		return nil, false
	}
	return s.views[row.manifest], true
}
