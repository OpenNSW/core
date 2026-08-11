// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestFixedSchemaClaims_KeysAreFolded enforces the invariant the collision
// guard depends on: lookups fold the candidate name, so a key that isn't
// already lowercase would be unreachable and silently stop guarding.
func TestFixedSchemaClaims_KeysAreFolded(t *testing.T) {
	for name := range fixedSchemaClaims {
		if folded := foldClaimName(name); folded != name {
			t.Errorf("fixedSchemaClaims key %q is not folded (want %q)", name, folded)
		}
	}
}

// TestTokenClaims_JSONBindingIsCaseInsensitive documents why the collision
// guard has to fold at all. encoding/json falls back to a case-insensitive
// match on struct tags, so these payload keys reach the fixed-schema fields
// even though a case-sensitive guard would not recognise them.
func TestTokenClaims_JSONBindingIsCaseInsensitive(t *testing.T) {
	var c tokenClaims
	raw := `{"Roles":["admin"],"Client_ID":"evil","GRANT_TYPE":"client_credentials","SCOPE":"a b"}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	roles, err := parseRolesClaim(defaultRolesClaim, c.Roles.value)
	if err != nil {
		t.Fatalf("parseRolesClaim: %v", err)
	}
	if len(roles) != 1 || roles[0] != "admin" {
		t.Fatalf(`"Roles" did not bind to tokenClaims.Roles: %#v`, roles)
	}
	if c.ClientID != "evil" {
		t.Fatalf(`"Client_ID" did not bind to tokenClaims.ClientID: %q`, c.ClientID)
	}
	if c.GrantType != ClientCredentialsGrant {
		t.Fatalf(`"GRANT_TYPE" did not bind to tokenClaims.GrantType: %q`, c.GrantType)
	}
	if len(c.Scopes) != 2 {
		t.Fatalf(`"SCOPE" did not bind to tokenClaims.Scopes: %#v`, c.Scopes)
	}
}

func TestParseRolesClaim(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		want    []string
		wantNil bool // sameScopes cannot tell nil from an empty slice; assert it explicitly
		wantErr bool
	}{
		{name: "absent", value: nil, want: nil, wantNil: true},
		{name: "array of strings", value: []any{"a", "b"}, want: []string{"a", "b"}},
		{name: "elements are trimmed", value: []any{" a ", "b"}, want: []string{"a", "b"}},
		{name: "empty array", value: []any{}, want: []string{}},
		// Deliberately stricter than ExtraClaims.Strings: space-splitting a
		// bare string would turn "Customs Officer" into two roles.
		{name: "space-delimited string", value: "a b", wantErr: true},
		{name: "bare string", value: "admin", wantErr: true},
		{name: "number", value: 42.0, wantErr: true},
		{name: "object", value: map[string]any{"a": true}, wantErr: true},
		{name: "mixed array", value: []any{"a", 1.0}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRolesClaim("roles", tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %#v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !sameScopes(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
			if (got == nil) != tt.wantNil {
				t.Fatalf("got nil = %v, want nil = %v (%#v)", got == nil, tt.wantNil, got)
			}
		})
	}
}

func TestRolesClaim(t *testing.T) {
	t.Run("custom claim populates roles", func(t *testing.T) {
		extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithRolesClaim("groups"))
		defer cleanup()

		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["groups"] = []string{"exporter", "broker"}
		// The literal "roles" claim is ignored once roles move elsewhere.
		claims["roles"] = []string{"should-be-ignored"}

		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if got := p.UserPrincipal.Roles; len(got) != 2 || got[0] != "exporter" || got[1] != "broker" {
			t.Fatalf("roles = %#v, want [exporter broker]", got)
		}
	})

	t.Run("applies to client principals too", func(t *testing.T) {
		extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithRolesClaim("groups"))
		defer cleanup()

		claims := newBaseClaims(ClientCredentialsGrant)
		claims["sub"] = "SOME_CLIENT"
		claims["groups"] = []string{"ingest"}

		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if got := p.ClientPrincipal.Roles; len(got) != 1 || got[0] != "ingest" {
			t.Fatalf("roles = %#v, want [ingest]", got)
		}
	})

	t.Run("absent custom claim yields no roles, not an error", func(t *testing.T) {
		extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithRolesClaim("groups"))
		defer cleanup()

		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID

		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("expected a user with no roles to authenticate, got %v", err)
		}
		if len(p.UserPrincipal.Roles) != 0 {
			t.Fatalf("roles = %#v, want none", p.UserPrincipal.Roles)
		}
	})

	t.Run("wrong-shaped custom claim rejects the token", func(t *testing.T) {
		// A silently empty Roles reads as "granted nothing", which fails OPEN
		// for any check phrased as "deny if the principal has role X".
		extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithRolesClaim("groups"))
		defer cleanup()

		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["groups"] = "exporter broker"

		if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims)); err == nil {
			t.Fatalf("expected a space-delimited roles string to be rejected")
		}
	})

	t.Run("remapping decouples from the literal roles claim", func(t *testing.T) {
		// With roles bound as []string this token was rejected by
		// encoding/json before the configured claim was ever read.
		extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithRolesClaim("groups"))
		defer cleanup()

		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["groups"] = []string{"exporter"}
		claims["roles"] = "legacy-string"

		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("expected the unused roles claim's shape to be irrelevant, got %v", err)
		}
		if got := p.UserPrincipal.Roles; len(got) != 1 || got[0] != "exporter" {
			t.Fatalf("roles = %#v, want [exporter]", got)
		}
	})

	t.Run("default claim still rejects a wrong shape", func(t *testing.T) {
		extractor, privateKey, cleanup := newTokenExtractor(t)
		defer cleanup()

		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["roles"] = "exporter"

		if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims)); err == nil {
			t.Fatalf("expected a bare-string roles claim to be rejected")
		}
	})
}

func TestRolesClaim_NameValidation(t *testing.T) {
	construct := func(opts ...Option) error {
		_, err := NewTokenExtractor("https://localhost/jwks", testIssuer, testClientID, []string{testClientID}, opts...)
		return err
	}

	t.Run("any case of roles is accepted", func(t *testing.T) {
		for _, name := range []string{"roles", "Roles", "ROLES"} {
			if err := construct(WithRolesClaim(name)); err != nil {
				t.Fatalf("WithRolesClaim(%q): %v", name, err)
			}
		}
	})

	t.Run("other fixed-schema names are rejected", func(t *testing.T) {
		for _, name := range []string{"scope", "sub", "client_id", "SCOPE", "exp"} {
			if err := construct(WithRolesClaim(name)); err == nil {
				t.Fatalf("expected WithRolesClaim(%q) to be rejected", name)
			}
		}
	})

	t.Run("blank is rejected", func(t *testing.T) {
		for _, name := range []string{"", "   "} {
			if err := construct(WithRolesClaim(name)); err == nil {
				t.Fatalf("expected WithRolesClaim(%q) to be rejected", name)
			}
		}
	})

	t.Run("namespaced name is accepted verbatim", func(t *testing.T) {
		if err := construct(WithRolesClaim("https://app.example.com/roles")); err != nil {
			t.Fatalf("expected a namespaced claim name to be accepted, got %v", err)
		}
	})

	t.Run("cannot also be declared as an extra claim, in either option order", func(t *testing.T) {
		orders := map[string][]Option{
			"roles first":   {WithRolesClaim("groups"), WithUserClaims(ClaimSpec{Optional: []string{"groups"}})},
			"claims first":  {WithUserClaims(ClaimSpec{Optional: []string{"groups"}}), WithRolesClaim("groups")},
			"client claims": {WithRolesClaim("groups"), WithClientClaims(ClaimSpec{Required: []string{"groups"}})},
		}
		for name, opts := range orders {
			t.Run(name, func(t *testing.T) {
				err := construct(opts...)
				if err == nil {
					t.Fatalf("expected the roles claim to be reserved")
				}
				if !strings.Contains(err.Error(), "groups") {
					t.Fatalf("error should name the offending claim, got %v", err)
				}
			})
		}
	})
}

// TestRolesClaim_NoDeclaredClaimsYieldsNilExtraClaims pins that remapping
// roles (which forces a payload decode) does not turn the "nothing declared"
// case from a nil ExtraClaims into an empty non-nil map.
func TestRolesClaim_NoDeclaredClaimsYieldsNilExtraClaims(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithRolesClaim("groups"))
	defer cleanup()

	claims := newBaseClaims(AuthorizationCodeGrant)
	claims["sub"] = testUserID
	claims["groups"] = []string{"exporter"}

	p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if p.UserPrincipal.ExtraClaims != nil {
		t.Fatalf("expected nil ExtraClaims, got %#v", p.UserPrincipal.ExtraClaims)
	}
}
