// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"fmt"

	"github.com/OpenNSW/core/shared/validation"
)

// Config carries yaml struct tags, so it can be embedded in a larger
// application config struct and populated generically (e.g. via
// yaml.Unmarshal).
type Config struct {
	JWKSURL               string `yaml:"jwksURL"`
	Issuer                string `yaml:"issuer"`
	Audience              string `yaml:"audience"`
	InsecureSkipTLSVerify bool   `yaml:"insecureSkipTLSVerify"`

	// RolesClaim overrides which claim carries the principal's roles, for
	// IdPs that do not emit a top-level "roles" claim. See WithRolesClaim.
	// Empty = "roles".
	RolesClaim string `yaml:"rolesClaim"`
}

func (c Config) Validate() error {
	if c.JWKSURL == "" {
		return fmt.Errorf("jwksURL is required")
	}
	if err := validation.HTTPURL("jwksURL", c.JWKSURL); err != nil {
		return err
	}
	if c.Issuer == "" {
		return fmt.Errorf("issuer is required")
	}
	if err := validation.HTTPURL("issuer", c.Issuer); err != nil {
		return err
	}
	if c.Audience == "" {
		return fmt.Errorf("audience is required")
	}

	return validateRolesClaimName(c.rolesClaim())
}

// rolesClaim resolves the configured roles claim name, treating an unset field
// as the default. A whitespace-only value is left as-is so Validate reports it
// as a typo rather than silently defaulting.
func (c Config) rolesClaim() string {
	if c.RolesClaim == "" {
		return defaultRolesClaim
	}
	return c.RolesClaim
}
