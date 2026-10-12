// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Ref is one artifact another artifact points at. It is satisfied when the same manifest lists
// ID under any of Kinds.
type Ref struct {
	ID    string
	Kinds []Kind
	// Field names where the reference was found, for the error message (e.g. "workflow_id").
	Field string
}

// Check parses one artifact's bytes, reporting a malformed artifact as an error, and returns the
// artifacts it points at. Adapter packages provide one per kind they own.
type Check func(raw []byte) ([]Ref, error)

// Checks maps a kind to its Check. A kind without one is only checked to exist.
type Checks map[Kind]Check

// validateConcurrency bounds how many artifact files Validate fetches at once.
const validateConcurrency = 8

// Validate fetches every artifact manifest v lists and runs the Check for its kind. It reports
// every problem found, not just the first: a missing file, an artifact its Check rejects, and a
// reference to an ID the same manifest does not list under an accepted kind.
//
// Run it on the current version at startup so a broken manifest stops the deploy instead of the
// first flow that reaches the broken artifact.
func (s *Set) Validate(ctx context.Context, v int, checks Checks) error {
	reg, err := s.Registry(ctx, v)
	if err != nil {
		return err
	}
	return validateRegistry(ctx, reg, ManifestPath(v), checks)
}

type rowRef struct {
	key  Key
	path string
}

func validateRegistry(ctx context.Context, reg *Registry, name string, checks Checks) error {
	rows := reg.rows()

	type result struct {
		refs []Ref
		err  error
	}
	results := make([]result, len(rows))

	sem := make(chan struct{}, validateConcurrency)
	var wg sync.WaitGroup
	for i, row := range rows {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			raw, err := reg.loader.Load(ctx, row.path)
			if err != nil {
				results[i].err = fmt.Errorf("load %q: %w", row.path, err)
				return
			}
			check, ok := checks[row.key.Kind]
			if !ok {
				return
			}
			refs, err := check(raw)
			if err != nil {
				results[i].err = fmt.Errorf("%q: %w", row.path, err)
				return
			}
			results[i].refs = refs
		}()
	}
	wg.Wait()

	var errs []error
	for i, row := range rows {
		label := fmt.Sprintf("%s/%s", row.key.ID, row.key.Kind)
		if row.key.Version != "" {
			label += fmt.Sprintf(" version %q", row.key.Version)
		}
		if results[i].err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", label, results[i].err))
			continue
		}
		for _, ref := range results[i].refs {
			if !reg.listsAny(ref.ID, ref.Kinds) {
				errs = append(errs, fmt.Errorf("%s: %s %q is not listed as %s", label, ref.Field, ref.ID, joinKinds(ref.Kinds)))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s has %d problem(s):\n%w", name, len(errs), errors.Join(errs...))
	}
	return nil
}

func joinKinds(kinds []Kind) string {
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = string(k)
	}
	return strings.Join(parts, " or ")
}

// rows returns every registered row, sorted by kind, id and version so reports are stable.
func (r *Registry) rows() []rowRef {
	var out []rowRef
	for ik, versions := range r.manifest {
		for v, e := range versions {
			out = append(out, rowRef{key: Key{ID: ik.ID, Kind: ik.Kind, Version: v}, path: e.path})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].key, out[j].key
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Version < b.Version
	})
	return out
}

// listsAny reports whether the registry lists id under any of kinds, at any version.
func (r *Registry) listsAny(id string, kinds []Kind) bool {
	for _, k := range kinds {
		if len(r.manifest[idKind{ID: id, Kind: k}]) > 0 {
			return true
		}
	}
	return false
}
