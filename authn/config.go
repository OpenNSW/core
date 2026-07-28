// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"fmt"

	"github.com/OpenNSW/core/shared/validation"
)

type Config struct {
	JWKSURL               string
	Issuer                string
	Audience              string
	ClientIDs             []string
	InsecureSkipTLSVerify bool

	// UserClaims declares extra JWT claims (beyond authn's fixed schema, e.g.
	// "email", "phone_number", "ouId", "ouHandle", "given_name") to extract
	// for user-principal (authorization_code grant) tokens. ClientClaims is
	// the client-credential (M2M) analogue. See WithUserClaims /
	// WithClientClaims. Zero value = no extra claims extracted.
	UserClaims   ClaimSpec
	ClientClaims ClaimSpec

	// RolesClaim overrides which claim carries the principal's roles, for
	// IdPs that do not emit a top-level "roles" claim. See WithRolesClaim.
	// Empty = "roles".
	RolesClaim string
}

func (c Config) Validate() error {
	if c.JWKSURL == "" {
		return fmt.Errorf("AUTH_JWKS_URL is required")
	}
	if err := validation.HTTPURL("AUTH_JWKS_URL", c.JWKSURL); err != nil {
		return err
	}
	if c.Issuer == "" {
		return fmt.Errorf("AUTH_ISSUER is required")
	}
	if err := validation.HTTPURL("AUTH_ISSUER", c.Issuer); err != nil {
		return err
	}
	if c.Audience == "" {
		return fmt.Errorf("AUTH_AUDIENCE is required")
	}

	if len(c.ClientIDs) == 0 {
		return fmt.Errorf("AUTH_CLIENT_IDS is required")
	}

	if err := validateRolesClaimName(c.rolesClaim()); err != nil {
		return err
	}
	for _, decl := range []struct {
		what  string
		names []string
	}{
		{"UserClaims.Optional", c.UserClaims.Optional},
		{"UserClaims.Required", c.UserClaims.Required},
		{"ClientClaims.Optional", c.ClientClaims.Optional},
		{"ClientClaims.Required", c.ClientClaims.Required},
	} {
		if err := validateClaimNames(decl.what, decl.names, c.rolesClaim()); err != nil {
			return err
		}
	}

	return nil
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
