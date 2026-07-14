// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package local

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Config holds configuration for the local filesystem loader.
//
// This is owned by the local package (mirroring temporal.Config), so the
// package controls the shape and semantics of its own configuration.
type Config struct {
	// Root is the directory that artifact paths are resolved against.
	Root string
}

// Validate ensures the local loader configuration is usable. It reports
// misconfiguration at construction time rather than deferring it to the
// first Load call.
func (c Config) Validate() error {
	if c.Root == "" {
		return fmt.Errorf("local loader: Root is required")
	}
	info, err := os.Stat(c.Root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("local loader: Root %q does not exist", c.Root)
		}
		return fmt.Errorf("local loader: stat Root %q: %w", c.Root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local loader: Root %q is not a directory", c.Root)
	}
	return nil
}
