// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import (
	"fmt"

	"github.com/OpenNSW/core/shared/validation"
)

// LocalConfig holds configuration for the local filesystem storage backend.
//
// LocalConfig carries yaml struct tags, so it can be embedded in a larger
// application config struct and populated generically (e.g. via
// yaml.Unmarshal).
type LocalConfig struct {
	// BaseDir is the directory files are stored under (created if absent).
	BaseDir string `yaml:"baseDir"`
	// PublicURL is the origin of the server that serves the driver's
	// content routes (e.g. http://localhost:8080). See
	// storage.LocalContentHandler.
	PublicURL string `yaml:"publicURL"`
	// RoutePrefix is the path the content routes sit under on that server,
	// e.g. "/files" for routes at /files/{key}/content. Empty means
	// DefaultLocalRoutePrefix ("/api/v1/storage").
	RoutePrefix string `yaml:"routePrefix"`
	// PutSecret signs presigned upload URLs for the local driver.
	PutSecret string `yaml:"putSecret"`
}

// Validate ensures the local storage configuration is usable.
func (c LocalConfig) Validate() error {
	if c.BaseDir == "" {
		return fmt.Errorf("local storage: BaseDir is required")
	}
	if c.PublicURL == "" {
		return fmt.Errorf("local storage: PublicURL is required")
	}
	if err := validation.HTTPURL("local storage: PublicURL", c.PublicURL); err != nil {
		return err
	}
	if c.PutSecret == "" {
		return fmt.Errorf("local storage: PutSecret is required")
	}
	if c.RoutePrefix != "" {
		if err := validateRoutePrefix(c.RoutePrefix); err != nil {
			return fmt.Errorf("local storage: %w", err)
		}
	}
	return nil
}
