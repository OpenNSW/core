// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// ManifestFilename is the fixed name of the manifest, resolved at the loader's
// root. The manifest lives alongside the artifacts it catalogs, so a single
// loader resolves both; consumers do not choose its location. Point the loader's
// base (Root/Bucket/Repo) at wherever the manifest and artifacts reside.
const ManifestFilename = "manifest.json"

type ManifestConfig struct {
	Artifacts []ManifestRow `json:"artifacts"`
}

type ManifestRow struct {
	ID      string `json:"id"`
	Kind    Kind   `json:"kind"`
	Version string `json:"version"` // "" allowed for unversioned
	Path    string `json:"path"`
}

// RegisterFromConfig applies every row via RegisterArtifact. Rows are validated
// so misconfiguration is caught at startup rather than at first access: id, kind,
// and path are all required (version may be "" for unversioned artifacts). There
// is no per-row loader — the registry has a single loader, so a row only names
// what to fetch, never how.
func RegisterFromConfig(r *Registry, cfg ManifestConfig) error {
	for i, row := range cfg.Artifacts {
		if row.ID == "" || row.Kind == "" || row.Path == "" {
			return fmt.Errorf("manifest row %d (%q/%q): id, kind, and path are all required", i, row.ID, row.Kind)
		}
		r.RegisterArtifact(row.ID, row.Kind, row.Version, row.Path)
	}
	slog.Info("artifact manifest registered", "count", len(cfg.Artifacts))
	return nil
}

// LoadManifest fetches and unmarshals the manifest from the artifact source
// itself, through the same loader that fetches the artifacts — so the catalog
// and its artifacts share one origin. The manifest always lives at
// ManifestFilename ("manifest.json") relative to the loader's root; its location
// is a fixed convention, not a parameter.
func LoadManifest(ctx context.Context, l Loader) (ManifestConfig, error) {
	var cfg ManifestConfig
	data, err := l.Load(ctx, ManifestFilename)
	if err != nil {
		return cfg, fmt.Errorf("load manifest %q: %w", ManifestFilename, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("unmarshal manifest %q: %w", ManifestFilename, err)
	}
	return cfg, nil
}
