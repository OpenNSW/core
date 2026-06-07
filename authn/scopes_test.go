package authn

import (
	"encoding/json"
	"testing"
)

func sameScopes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The OAuth2 "scope" claim is a single space-delimited string (RFC 6749 §3.3);
// it must unmarshal into a slice without error.
func TestSpaceDelimitedScope_Unmarshal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"space-delimited string", `{"scope":"nsw:task:read nsw:storage:read"}`, []string{"nsw:task:read", "nsw:storage:read"}},
		{"extra whitespace", `{"scope":"  a   b "}`, []string{"a", "b"}},
		{"empty string", `{"scope":""}`, nil},
		{"absent", `{}`, nil},
		{"defensive array", `{"scope":["a","b"]}`, []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c tokenClaims
			if err := json.Unmarshal([]byte(tc.in), &c); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := []string(c.Scopes); !sameScopes(got, tc.want) {
				t.Fatalf("scopes = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSpaceDelimitedScope_InvalidType(t *testing.T) {
	var c tokenClaims
	if err := json.Unmarshal([]byte(`{"scope":123}`), &c); err == nil {
		t.Fatal("expected error unmarshaling a numeric scope claim")
	}
}

// End-to-end through the parser: scope claim and client roles/scopes surface on
// the principal and flow through to the AuthContext.
func TestExtractPrincipal_SurfacesScopesAndRoles(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	t.Run("user token scopes", func(t *testing.T) {
		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["email"] = testEmail
		claims["ouId"] = testOUID
		claims["ouHandle"] = testOUHandle
		claims["roles"] = []string{"Trader"}
		claims["scope"] = "nsw:task:read nsw:storage:read"

		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if p.UserPrincipal == nil {
			t.Fatal("expected user principal")
		}
		if !sameScopes(p.UserPrincipal.Scopes, []string{"nsw:task:read", "nsw:storage:read"}) {
			t.Fatalf("user scopes = %v", p.UserPrincipal.Scopes)
		}
	})

	t.Run("client token roles and scopes", func(t *testing.T) {
		claims := newBaseClaims(ClientCredentialsGrant)
		claims["sub"] = testClientID
		claims["roles"] = []string{"AgencyM2M"}
		claims["scope"] = "nsw:task:write nsw:consignment:read"

		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if p.ClientPrincipal == nil {
			t.Fatal("expected client principal")
		}
		if !sameScopes(p.ClientPrincipal.Scopes, []string{"nsw:task:write", "nsw:consignment:read"}) {
			t.Fatalf("client scopes = %v", p.ClientPrincipal.Scopes)
		}
		if !sameScopes(p.ClientPrincipal.Roles, []string{"AgencyM2M"}) {
			t.Fatalf("client roles = %v", p.ClientPrincipal.Roles)
		}
	})
}

func TestBuildAuthContext_SurfacesScopesAndRoles(t *testing.T) {
	uc := buildAuthContext(&Principal{
		Type: UserPrincipalType,
		UserPrincipal: &UserPrincipal{
			UserID: "u1", Email: testEmail, OUID: testOUID, OUHandle: testOUHandle,
			Roles: []string{"Trader"}, Scopes: []string{"nsw:task:read"},
		},
	})
	if uc.User == nil || !sameScopes(uc.User.Scopes, []string{"nsw:task:read"}) {
		t.Fatalf("user scopes not surfaced: %#v", uc.User)
	}

	cc := buildAuthContext(&Principal{
		Type: ClientPrincipalType,
		ClientPrincipal: &ClientPrincipal{
			ClientID: "NPQS_TO_NSW", Roles: []string{"AgencyM2M"}, Scopes: []string{"nsw:task:write"},
		},
	})
	if cc.Client == nil || !sameScopes(cc.Client.Scopes, []string{"nsw:task:write"}) || !sameScopes(cc.Client.Roles, []string{"AgencyM2M"}) {
		t.Fatalf("client roles/scopes not surfaced: %#v", cc.Client)
	}
}

// Compile-time proof that *AuthContext satisfies the accessor seam used by authz.
var _ interface {
	Type() PrincipalType
	Subject() string
	Roles() []string
	Scopes() []string
} = (*AuthContext)(nil)

func TestAuthContext_AccessorSeam(t *testing.T) {
	user := &AuthContext{User: &UserContext{ID: "u1", IDPUserID: "idp1", Roles: []string{"Trader"}, Scopes: []string{"nsw:task:read"}}}
	if user.Type() != UserPrincipalType {
		t.Fatalf("user Type = %q", user.Type())
	}
	if user.Subject() != "u1" {
		t.Fatalf("user Subject = %q", user.Subject())
	}
	if !sameScopes(user.Roles(), []string{"Trader"}) || !sameScopes(user.Scopes(), []string{"nsw:task:read"}) {
		t.Fatalf("user roles/scopes = %v / %v", user.Roles(), user.Scopes())
	}

	// Subject falls back to IdP user ID when resolved ID is empty.
	if got := (&AuthContext{User: &UserContext{IDPUserID: "idp2"}}).Subject(); got != "idp2" {
		t.Fatalf("subject fallback = %q, want idp2", got)
	}

	client := &AuthContext{Client: &ClientContext{ClientID: "NPQS_TO_NSW", Roles: []string{"AgencyM2M"}, Scopes: []string{"nsw:task:write"}}}
	if client.Type() != ClientPrincipalType || client.Subject() != "NPQS_TO_NSW" {
		t.Fatalf("client Type/Subject = %q / %q", client.Type(), client.Subject())
	}
	if !sameScopes(client.Roles(), []string{"AgencyM2M"}) || !sameScopes(client.Scopes(), []string{"nsw:task:write"}) {
		t.Fatalf("client roles/scopes = %v / %v", client.Roles(), client.Scopes())
	}

	// Nil-safe: empty context and nil receiver return zero values.
	for _, a := range []*AuthContext{{}, nil} {
		if a.Type() != "" || a.Subject() != "" || a.Roles() != nil || a.Scopes() != nil {
			t.Fatalf("expected zero values for %#v", a)
		}
	}
}
