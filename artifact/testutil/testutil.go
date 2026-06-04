// Package testutil provides shared test helpers for packages that work with the artifact registry.
package testutil

import (
	"context"

	"github.com/OpenNSW/core/artifact"
)

// MemLoader is an in-memory artifact.Loader for tests.
// Populate it with path → raw bytes entries and register it with a Registry.
type MemLoader map[string][]byte

func (m MemLoader) Load(_ context.Context, path string) ([]byte, error) {
	if b, ok := m[path]; ok {
		return b, nil
	}
	return nil, artifact.ErrNotFound
}
