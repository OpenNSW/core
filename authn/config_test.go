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
	}

	// Each error names the setting by its yaml key, so a config file author
	// can find it.
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{name: "valid config", config: valid},
		{name: "missing jwks url", config: Config{Issuer: valid.Issuer, Audience: valid.Audience}, wantErr: "jwksURL is required"},
		{name: "invalid jwks url", config: Config{JWKSURL: "jwks", Issuer: valid.Issuer, Audience: valid.Audience}, wantErr: "jwksURL must be a valid absolute URL"},
		{name: "missing issuer", config: Config{JWKSURL: valid.JWKSURL, Audience: valid.Audience}, wantErr: "issuer is required"},
		{name: "invalid issuer", config: Config{JWKSURL: valid.JWKSURL, Issuer: "ftp://localhost", Audience: valid.Audience}, wantErr: "issuer must use http or https"},
		{name: "missing audience", config: Config{JWKSURL: valid.JWKSURL, Issuer: valid.Issuer}, wantErr: "audience is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("Validate() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
