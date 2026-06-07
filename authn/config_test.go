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
