// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact

import (
	"context"
	"encoding/json"
	"fmt"
)

// ManifestFilename is the unversioned manifest: the single index an artifacts repository keeps
// of its current state. A source that has no manifest_v1.json loads it as version 1 (see
// LoadSet). A release publishes the same index as an immutable manifest_vN.json.
const ManifestFilename = "manifest.json"

// ManifestPath returns the path of manifest version v, resolved at the loader's root:
// manifest_v1.json, manifest_v2.json, and so on. Manifests live alongside the artifacts they
// catalog, so one loader resolves both; consumers do not choose their location. Point the
// loader's base (Root/Bucket/Repo) at the directory that holds them.
//
// A manifest is immutable once published. A change to any artifact is a new file, listed by a
// new manifest with the next version number; see Set.
func ManifestPath(v int) string {
	return fmt.Sprintf("manifest_v%d.json", v)
}

type ManifestConfig struct {
	Artifacts []ManifestRow `json:"artifacts"`
}

// ManifestRow names the file one artifact resolves to in a manifest.
//
// Version is optional. A row without one is the artifact for that manifest: the manifest
// itself is the version. A row with one is an artifact requested by explicit version (static
// data a form fetches by ID and version, for example); such a row must point at the same path
// in every manifest that lists it, so the version alone identifies the content.
type ManifestRow struct {
	ID      string `json:"id"`
	Kind    Kind   `json:"kind"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

// RegisterFromConfig applies every row via RegisterArtifact. Rows are validated so
// misconfiguration is caught at startup rather than at first access: id, kind, and path are all
// required, and no two rows may share an id, kind and version. There is no per-row loader — the
// registry has a single loader, so a row only names what to fetch, never how.
func RegisterFromConfig(r *Registry, cfg ManifestConfig) error {
	seen := make(map[Key]int, len(cfg.Artifacts))
	for i, row := range cfg.Artifacts {
		if row.ID == "" || row.Kind == "" || row.Path == "" {
			return fmt.Errorf("manifest row %d (%q/%q): id, kind, and path are all required", i, row.ID, row.Kind)
		}
		k := Key{ID: row.ID, Kind: row.Kind, Version: row.Version}
		if first, dup := seen[k]; dup {
			return fmt.Errorf("manifest rows %d and %d both list %s/%s version %q", first, i, row.ID, row.Kind, row.Version)
		}
		seen[k] = i
	}
	for _, row := range cfg.Artifacts {
		r.RegisterArtifact(row.ID, row.Kind, row.Version, row.Path)
	}
	return nil
}

// LoadManifest fetches and unmarshals manifest version v from the artifact source itself,
// through the same loader that fetches the artifacts, so the catalog and its artifacts share
// one origin. A missing manifest returns an error matching ErrNotFound.
func LoadManifest(ctx context.Context, l Loader, v int) (ManifestConfig, error) {
	var cfg ManifestConfig
	data, err := l.Load(ctx, ManifestPath(v))
	if err != nil {
		return cfg, fmt.Errorf("load manifest %q: %w", ManifestPath(v), err)
	}
	return parseManifest(ManifestPath(v), data)
}

func parseManifest(name string, data []byte) (ManifestConfig, error) {
	var cfg ManifestConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ManifestConfig{}, fmt.Errorf("unmarshal manifest %q: %w", name, err)
	}
	return cfg, nil
}
