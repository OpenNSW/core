package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type ManifestConfig struct {
	Artifacts []ManifestRow `json:"artifacts" yaml:"artifacts"`
}

type ManifestRow struct {
	ID      string `json:"id"      yaml:"id"`
	Kind    Kind   `json:"kind"    yaml:"kind"`
	Version string `json:"version" yaml:"version"` // "" allowed for unversioned
	Loader  string `json:"loader"  yaml:"loader"`  // loader type name, e.g. "s3"
	Path    string `json:"path"    yaml:"path"`
}

// RegisterFromConfig applies every row via RegisterArtifact. It does NOT register
// loaders (those need live clients/credentials, wired in code). Return an error
// if a row references a loader type not yet registered, so misconfiguration is
// caught at startup.
func RegisterFromConfig(r *Registry, cfg ManifestConfig) error {
	for _, row := range cfg.Artifacts {
		if _, ok := r.loaders[row.Loader]; !ok {
			return fmt.Errorf("manifest row references unregistered loader type %q for artifact %s/%s", row.Loader, row.ID, row.Kind)
		}
		r.RegisterArtifact(row.ID, row.Kind, row.Version, row.Loader, row.Path)
	}
	return nil
}

// LoadManifestFile reads + unmarshals a JSON or YAML manifest file.
func LoadManifestFile(path string) (ManifestConfig, error) {
	var cfg ManifestConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read manifest file: %w", err)
	}

	ext := filepath.Ext(path)
	if ext == ".yaml" || ext == ".yml" {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("unmarshal manifest YAML: %w", err)
		}
	} else {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("unmarshal manifest JSON: %w", err)
		}
	}

	return cfg, nil
}
