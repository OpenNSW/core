package authn

import (
	"slices"
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		JWKSURL:  "https://idp.example.com/oauth2/jwks",
		Issuer:   "https://idp.example.com",
		Audience: "https://api.nsw-srilanka.local",
	}
}

// Every claim Principal surfaces must also be declared to core/authn, or it is
// silently never extracted and the field reads as empty at runtime. This test is
// the link between the two halves.
func TestConfig_DeclaresEveryClaimPrincipalReads(t *testing.T) {
	spec := validConfig().coreConfig().UserClaims
	declared := slices.Concat(spec.Required, spec.Optional)

	for _, claim := range []string{claimEmail, claimPhoneNumber, claimOUID, claimOUHandle} {
		if !slices.Contains(declared, claim) {
			t.Errorf("claim %q is read by principalFromAuthContext but never declared to core/authn", claim)
		}
	}
}

// ouHandle backs the task-ownership gate and the user_records.ou_handle NOT NULL
// constraint, so a token missing it must be rejected at the auth boundary rather
// than surfacing as a database error deeper in the request.
func TestConfig_OUHandleIsRequired(t *testing.T) {
	spec := validConfig().coreConfig().UserClaims
	if !slices.Contains(spec.Required, claimOUHandle) {
		t.Errorf("%q must be a required claim, got required=%v optional=%v", claimOUHandle, spec.Required, spec.Optional)
	}
	if !slices.Contains(spec.Optional, claimPhoneNumber) {
		t.Errorf("%q should be optional, got required=%v optional=%v", claimPhoneNumber, spec.Required, spec.Optional)
	}
}

func TestConfig_Validate(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("expected a valid config, got %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing jwks url", func(c *Config) { c.JWKSURL = "" }},
		{"missing issuer", func(c *Config) { c.Issuer = "" }},
		{"missing audience", func(c *Config) { c.Audience = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

// builtinUserClaims is what is declared to core/authn and what the extra-claim
// guard reserves, so it must list every claim Principal reads onto a named
// field.
func TestBuiltinUserClaims_ListsEveryClaimConstant(t *testing.T) {
	want := []string{claimEmail, claimOUHandle, claimOUID, claimPhoneNumber}
	if got := slices.Sorted(slices.Values(builtinUserClaimNames)); !slices.Equal(got, want) {
		t.Fatalf("builtinUserClaimNames = %v, want %v", got, want)
	}
}

// A deployment's extras must reach core/authn alongside the built-ins, or the
// claim is silently never extracted. User and client declarations stay
// separate, and a deployment with no extras declares nothing for clients.
func TestConfig_DeclaresExtraClaims(t *testing.T) {
	cfg := validConfig()
	cfg.ExtraUserClaims = ClaimSpec{Required: []string{"tenant"}, Optional: []string{"department"}}
	cfg.ExtraClientClaims = ClaimSpec{Optional: []string{"agency"}}

	core := cfg.coreConfig()
	for _, name := range []string{claimEmail, claimOUID, claimOUHandle, "tenant"} {
		if !slices.Contains(core.UserClaims.Required, name) {
			t.Errorf("user claim %q should be required, got %v", name, core.UserClaims.Required)
		}
	}
	for _, name := range []string{claimPhoneNumber, "department"} {
		if !slices.Contains(core.UserClaims.Optional, name) {
			t.Errorf("user claim %q should be optional, got %v", name, core.UserClaims.Optional)
		}
	}
	if len(core.ClientClaims.Required) != 0 || !slices.Equal(core.ClientClaims.Optional, []string{"agency"}) {
		t.Errorf("unexpected client claims: %+v", core.ClientClaims)
	}
	if slices.Contains(slices.Concat(core.UserClaims.Required, core.UserClaims.Optional), "agency") {
		t.Error("a client claim must not be extracted from user tokens")
	}

	if got := validConfig().coreConfig().ClientClaims; len(got.Required)+len(got.Optional) != 0 {
		t.Errorf("no extras configured, but client claims were declared: %+v", got)
	}
}

func TestConfig_ValidateExtraClaims(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr []string // substrings the error must contain; nil means valid
	}{
		{"no extras", func(*Config) {}, nil},
		{"valid user and client extras", func(c *Config) {
			c.ExtraUserClaims = ClaimSpec{Required: []string{"tenant"}, Optional: []string{"department", "https://x.example/grade"}}
			c.ExtraClientClaims = ClaimSpec{Optional: []string{"agency"}}
		}, nil},
		// The built-ins are only read from user tokens, so a client may list one.
		{"built-in name is fine for clients", func(c *Config) {
			c.ExtraClientClaims = ClaimSpec{Optional: []string{claimEmail}}
		}, nil},

		{"built-in in required", func(c *Config) {
			c.ExtraUserClaims = ClaimSpec{Required: []string{claimOUHandle}}
		}, []string{"authn.extraUserClaims.required", claimOUHandle}},
		{"built-in in optional", func(c *Config) {
			c.ExtraUserClaims = ClaimSpec{Optional: []string{claimEmail}}
		}, []string{"authn.extraUserClaims.optional", claimEmail}},
		{"built-in with different case and padding", func(c *Config) {
			c.ExtraUserClaims = ClaimSpec{Optional: []string{" OUHANDLE "}}
		}, []string{"authn.extraUserClaims.optional", "OUHANDLE"}},
		{"every built-in is reported at once", func(c *Config) {
			c.ExtraUserClaims = ClaimSpec{Optional: []string{claimPhoneNumber, claimOUID}}
		}, []string{claimPhoneNumber, claimOUID}},

		{"blank user name", func(c *Config) {
			c.ExtraUserClaims = ClaimSpec{Optional: []string{"department", "  "}}
		}, []string{"authn.extraUserClaims.optional", "empty claim name"}},
		{"blank client name", func(c *Config) {
			c.ExtraClientClaims = ClaimSpec{Required: []string{""}}
		}, []string{"authn.extraClientClaims.required", "empty claim name"}},

		// Names core/authn already binds, rejected by core/authn's own guard,
		// which names the declaration (UserClaims or ClientClaims) it came from.
		{"user scope", func(c *Config) {
			c.ExtraUserClaims = ClaimSpec{Optional: []string{"scope"}}
		}, []string{"UserClaims.Optional", "scope"}},
		{"user roles", func(c *Config) {
			c.ExtraUserClaims = ClaimSpec{Required: []string{"roles"}}
		}, []string{"UserClaims.Required", "roles"}},
		{"client registered claim, case-folded", func(c *Config) {
			c.ExtraClientClaims = ClaimSpec{Optional: []string{"Client_Id"}}
		}, []string{"ClientClaims.Optional", "Client_Id"}},
		{"client sub", func(c *Config) {
			c.ExtraClientClaims = ClaimSpec{Required: []string{"sub"}}
		}, []string{"ClientClaims.Required", "sub"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("expected a valid config, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected a validation error")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should mention %q", err, want)
				}
			}
		})
	}
}

// A valid declaration must also be accepted by core/authn when the manager is
// built, not just by Validate.
func TestNewManager_AcceptsExtraClaims(t *testing.T) {
	cfg := validConfig()
	cfg.ExtraUserClaims = ClaimSpec{Optional: []string{"department"}}
	cfg.ExtraClientClaims = ClaimSpec{Optional: []string{"agency"}}

	m, err := NewManager(nil, cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	_ = m.Close()
}
