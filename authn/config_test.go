// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"testing"
)

func TestConfigValidate(t *testing.T) {
	valid := Config{
		JWKSURL:  "https://localhost/jwks",
		Issuer:   "https://localhost/token",
		Audience: "TRADER_PORTAL_APP",
		ClientIDs: []string{
			"TRADER_PORTAL_APP",
		},
	}

	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{name: "valid config", config: valid},
		{name: "missing jwks url", config: Config{Issuer: valid.Issuer, Audience: valid.Audience, ClientIDs: valid.ClientIDs}, wantErr: true},
		{name: "missing issuer", config: Config{JWKSURL: valid.JWKSURL, Audience: valid.Audience, ClientIDs: valid.ClientIDs}, wantErr: true},
		{name: "missing audience", config: Config{JWKSURL: valid.JWKSURL, Issuer: valid.Issuer, ClientIDs: valid.ClientIDs}, wantErr: true},
		{name: "missing client ids", config: Config{JWKSURL: valid.JWKSURL, Issuer: valid.Issuer, Audience: valid.Audience}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

func TestConfigValidate_RejectsFixedSchemaExtraClaim(t *testing.T) {
	valid := Config{
		JWKSURL:   "https://localhost/jwks",
		Issuer:    "https://localhost/token",
		Audience:  "TRADER_PORTAL_APP",
		ClientIDs: []string{"TRADER_PORTAL_APP"},
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"optional user claim", func(c *Config) { c.UserClaims.Optional = []string{"scope"} }},
		{"required user claim", func(c *Config) { c.UserClaims.Required = []string{"client_id"} }},
		{"optional client claim", func(c *Config) { c.ClientClaims.Optional = []string{"roles"} }},
		{"required client claim", func(c *Config) { c.ClientClaims.Required = []string{"sub"} }},
		// Case variants: encoding/json binds struct tags case-insensitively,
		// so these still land in the fixed-schema fields and must be rejected.
		{"case-variant user claim", func(c *Config) { c.UserClaims.Optional = []string{"Roles"} }},
		{"case-variant client claim", func(c *Config) { c.ClientClaims.Optional = []string{"Client_Id"} }},
		{"blank claim name", func(c *Config) { c.UserClaims.Required = []string{"email", "  "} }},
		{"roles claim collides with extra claim", func(c *Config) {
			c.RolesClaim = "groups"
			c.UserClaims.Optional = []string{"groups"}
		}},
		{"roles claim is a fixed-schema name", func(c *Config) { c.RolesClaim = "scope" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected error for fixed-schema claim declaration")
			}
		})
	}
}

// TestConfigValidate_AllowsFormerlyFixedClaims locks in the schema shrink at
// the Config layer too: email/phone_number/ouId/ouHandle used to be
// hardcoded fixed-schema fields and are now legitimate extra-claim
// declarations.
func TestConfigValidate_AllowsFormerlyFixedClaims(t *testing.T) {
	cfg := Config{
		JWKSURL:    "https://localhost/jwks",
		Issuer:     "https://localhost/token",
		Audience:   "TRADER_PORTAL_APP",
		ClientIDs:  []string{"TRADER_PORTAL_APP"},
		UserClaims: ClaimSpec{Optional: []string{"email", "phone_number", "ouId", "ouHandle", "given_name"}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

// TestConfigValidate_AllowsNamespacedClaimNames locks in that a claim name is
// matched exactly rather than parsed: Auth0-style namespaced claims contain
// dots and slashes and must not be mistaken for a nested path.
func TestConfigValidate_AllowsNamespacedClaimNames(t *testing.T) {
	cfg := Config{
		JWKSURL:    "https://localhost/jwks",
		Issuer:     "https://localhost/token",
		Audience:   "TRADER_PORTAL_APP",
		ClientIDs:  []string{"TRADER_PORTAL_APP"},
		RolesClaim: "https://app.example.com/roles",
		UserClaims: ClaimSpec{Optional: []string{"https://app.example.com/department"}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}
