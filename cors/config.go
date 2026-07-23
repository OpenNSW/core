// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package cors

import (
	"fmt"

	"github.com/OpenNSW/core/internal/validation"
)

type Config struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}

func (c Config) Validate() error {
	if len(c.AllowedOrigins) == 0 {
		return fmt.Errorf("CORS_ALLOWED_ORIGINS is required")
	}
	for _, origin := range c.AllowedOrigins {
		if origin == "*" {
			if c.AllowCredentials {
				return fmt.Errorf("wildcard origin '*' is not allowed when AllowCredentials is true")
			}
			continue
		}
		if err := validation.HTTPURL("CORS_ALLOWED_ORIGINS", origin); err != nil {
			return err
		}
	}
	return nil
}
