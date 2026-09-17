// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package temporal

import (
	"fmt"

	"github.com/OpenNSW/core/shared/validation"
)

// Config holds configuration required to connect to Temporal.
//
// This is owned by the temporal package (similar to other internal packages),
// so the package controls the shape/semantics of its configuration.
//
// Host/Port are kept separate to make configuration via environment variables
// easier and more explicit.
type Config struct {
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	Namespace string `yaml:"namespace"`
}

// Validate ensures the Temporal configuration is usable.
func (c Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("temporal host is required")
	}
	if err := validation.TCPPort("temporal port", c.Port); err != nil {
		return err
	}
	if c.Namespace == "" {
		return fmt.Errorf("temporal namespace is required")
	}
	return nil
}
