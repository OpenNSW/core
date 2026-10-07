// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package cors

import (
	"fmt"

	"github.com/OpenNSW/core/shared/validation"
)

// Config carries yaml struct tags, so it can be embedded in a larger
// application config struct and populated generically (e.g. via
// yaml.Unmarshal).
type Config struct {
	AllowedOrigins   []string `yaml:"allowedOrigins"`
	AllowedMethods   []string `yaml:"allowedMethods"`
	AllowedHeaders   []string `yaml:"allowedHeaders"`
	AllowCredentials bool     `yaml:"allowCredentials"`
	MaxAge           int      `yaml:"maxAge"`
}

func (c Config) Validate() error {
	if len(c.AllowedOrigins) == 0 {
		return fmt.Errorf("allowedOrigins is required")
	}
	for _, origin := range c.AllowedOrigins {
		if origin == "*" {
			if c.AllowCredentials {
				return fmt.Errorf("allowedOrigins cannot contain the wildcard '*' when allowCredentials is true")
			}
			continue
		}
		if err := validation.HTTPURL("allowedOrigins", origin); err != nil {
			return err
		}
	}
	return nil
}
