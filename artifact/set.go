// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// reprobeInterval is how long Registry waits after a probe for newer manifests found none
// before it probes again. A flow pinned to a version that was never published retries its
// steps; without it, each retry would hold the write lock for a loader round trip.
const reprobeInterval = 10 * time.Second

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
// so versions must be contiguous. A Set is safe for concurrent use.
type Set struct {
	loader Loader

	// probeMu serializes on-demand probes for newer manifests. It is held across the loader
	// round trips; mu is taken only to read state and to install what a probe fetched, so a
	// probe never blocks lookups of versions already loaded.
	probeMu sync.Mutex

	mu      sync.RWMutex
	views   map[int]*Registry
	latest  int
	current int

	// lastEmptyProbe is when Registry last probed for newer manifests and found none.
	lastEmptyProbe time.Time
	now            func() time.Time

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
// cannot stop a rollback to v. Newer manifests still load on demand, for a flow that another
// replica started on one.
func WithPinnedVersion(v int) SetOption {
	return func(o *setOptions) { o.pin = v }
}

// LoadSet loads every manifest the loader can see, starting at manifest_v1.json, and picks the
// current version. Loading stops at the first version whose manifest the loader reports as
// ErrNotFound, or at the pinned version; any other load error fails the call, so a transient
// fault never silently hides the newer manifests. Each manifest is checked as
// RegisterFromConfig checks it, and explicitly versioned rows are checked for a consistent path
// across manifests. Artifact files themselves are not fetched.
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

	s := &Set{
		loader:   loader,
		views:    make(map[int]*Registry),
		explicit: make(map[Key]explicitRow),
		now:      time.Now,
	}
	cfgs, err := fetchManifests(ctx, loader, 1, o.pin)
	if err != nil {
		return nil, err
	}
	for i, cfg := range cfgs {
		if err := s.add(i+1, cfg); err != nil {
			return nil, err
		}
	}
	if s.latest == 0 {
		return nil, fmt.Errorf("%w: no %s found", ErrUnknownManifestVersion, ManifestPath(1))
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
// error, with that error. It touches no Set state, so it runs without a Set lock.
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

// add registers manifest v. The caller must hold s.mu for writing, or own s exclusively.
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
// pinned one. It does not move when Registry loads a newer manifest on demand; a new manifest
// becomes current when the process reloads the set.
func (s *Set) Current() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Latest returns the highest manifest version loaded so far.
func (s *Set) Latest() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest
}

// Versions returns every loaded manifest version, ascending.
func (s *Set) Versions() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	vs := make([]int, 0, len(s.views))
	for v := range s.views {
		vs = append(vs, v)
	}
	slices.Sort(vs)
	return vs
}

// Registry returns the registry for manifest version v.
//
// A version above the highest loaded one is loaded on demand. Replicas of a service load their
// sets at different times, so a flow pinned by a replica that started after a manifest was
// published can run its next step on a replica that started before it.
func (s *Set) Registry(ctx context.Context, v int) (*Registry, error) {
	if v <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrUnknownManifestVersion, v)
	}
	s.mu.RLock()
	reg, ok := s.views[v]
	s.mu.RUnlock()
	if ok {
		return reg, nil
	}

	if err := s.probe(ctx, v); err != nil {
		return nil, err
	}
	s.mu.RLock()
	reg, ok = s.views[v]
	latest := s.latest
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %d (highest published is %d)", ErrUnknownManifestVersion, v, latest)
	}
	return reg, nil
}

// probe loads manifests above the highest loaded one, up to version upTo (0: until the loader
// reports one missing). It returns nil when there was nothing to load. One probe runs at a time
// (a caller that waited may find the work done), it holds no lock that lookups wait on while it
// fetches, and after a probe that loads nothing new, whatever the reason, the next one waits
// reprobeInterval, so retries of a pinned flow cannot turn into a loader round trip each.
func (s *Set) probe(ctx context.Context, upTo int) error {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	s.mu.RLock()
	latest, lastEmpty := s.latest, s.lastEmptyProbe
	s.mu.RUnlock()
	if upTo != 0 && upTo <= latest {
		return nil // loaded while this caller waited
	}
	if !lastEmpty.IsZero() && s.now().Sub(lastEmpty) < reprobeInterval {
		return nil
	}

	// Fetch only up to upTo: a later manifest, malformed or not, is no reason to fail this one.
	cfgs, fetchErr := fetchManifests(ctx, s.loader, latest+1, upTo)

	s.mu.Lock()
	var addErr error
	for i, cfg := range cfgs {
		if addErr = s.add(latest+1+i, cfg); addErr != nil {
			break
		}
	}
	newLatest := s.latest
	reached := upTo != 0 && newLatest >= upTo
	if reached || (upTo == 0 && newLatest > latest && addErr == nil && fetchErr == nil) {
		s.lastEmptyProbe = time.Time{}
	} else {
		s.lastEmptyProbe = s.now()
	}
	s.mu.Unlock()

	if newLatest > latest {
		slog.InfoContext(ctx, "artifact manifests loaded on demand", "from", latest+1, "latest", newLatest)
	}
	if reached {
		return nil
	}
	if addErr != nil {
		return addErr
	}
	return fetchErr
}

// CurrentRegistry returns the registry for the current version.
func (s *Set) CurrentRegistry() *Registry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.views[s.current]
}

// GetExplicit fetches an explicitly versioned artifact without a manifest version, from
// whichever manifest lists it. LoadSet guarantees every manifest that lists an (id, kind,
// version) points it at the same path, so the result does not depend on which one is used.
// Use it for artifacts requested by explicit version outside any flow, such as static data.
//
// A version no loaded manifest lists makes it probe for newer manifests (backing off as
// Registry does): a form in a flow that a newer replica started can ask for a version only a
// manifest this replica has not loaded lists.
func GetExplicit[T Artifact](ctx context.Context, s *Set, id, version string) (T, error) {
	var zero T
	kind := kindOf[T]()
	if version == "" {
		return zero, fmt.Errorf("%w: %s/%s needs an explicit version", ErrNotFound, id, kind)
	}
	key := Key{ID: id, Kind: kind, Version: version}
	reg, ok := s.explicitRegistry(key)
	if !ok {
		if err := s.probe(ctx, 0); err != nil {
			return zero, fmt.Errorf("%w: %s/%s version %q (probing newer manifests: %v)", ErrNotFound, id, kind, version, err)
		}
		if reg, ok = s.explicitRegistry(key); !ok {
			return zero, fmt.Errorf("%w: %s/%s version %q", ErrNotFound, id, kind, version)
		}
	}
	return Get[T](ctx, reg, id, version)
}

// explicitRegistry returns a registry of a loaded manifest that lists key.
func (s *Set) explicitRegistry(key Key) (*Registry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.explicit[key]
	if !ok {
		return nil, false
	}
	return s.views[row.manifest], true
}
