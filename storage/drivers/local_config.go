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
	// PublicURL is the base URL files are served from.
	PublicURL string `yaml:"publicURL"`
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
	return nil
}
