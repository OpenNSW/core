// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestExtraClaims_String(t *testing.T) {
	tests := []struct {
		name string
		c    ExtraClaims
		key  string
		want string
	}{
		{"present string", ExtraClaims{"email": "a@b.com"}, "email", "a@b.com"},
		{"absent key", ExtraClaims{}, "email", ""},
		{"wrong type number", ExtraClaims{"email": 42.0}, "email", ""},
		{"wrong type array", ExtraClaims{"email": []any{"a"}}, "email", ""},
		{"nil map", nil, "email", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.String(tt.key); got != tt.want {
				t.Fatalf("String(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestExtraClaims_Strings(t *testing.T) {
	tests := []struct {
		name string
		c    ExtraClaims
		key  string
		want []string
	}{
		{"native array", ExtraClaims{"roles": []any{"a", "b"}}, "roles", []string{"a", "b"}},
		{"space-delimited string", ExtraClaims{"roles": "a b"}, "roles", []string{"a", "b"}},
		{"absent key", ExtraClaims{}, "roles", nil},
		{"array with non-string element", ExtraClaims{"roles": []any{"a", 1.0}}, "roles", nil},
		{"non-string non-array value", ExtraClaims{"roles": 42.0}, "roles", nil},
		{"nil map", nil, "roles", nil},
		// Not produced by JSON decoding, but hand-built literals hit it.
		{"native string slice", ExtraClaims{"roles": []string{"a", "b"}}, "roles", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.c.Strings(tt.key)
			if !sameScopes(got, tt.want) {
				t.Fatalf("Strings(%q) = %#v, want %#v", tt.key, got, tt.want)
			}
		})
	}
}

func TestDecodeJWTPayload(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		errSubstr string
	}{
		{"wrong segment count", "a.b", "malformed jwt: expected 3 segments"},
		{"invalid base64", "a.@@@.c", "failed to decode jwt payload segment"},
		{"invalid json", "a." + base64.RawURLEncoding.EncodeToString([]byte("not-json")) + ".c", "failed to unmarshal jwt payload"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeJWTPayload(tt.token)
			if err == nil || !strings.Contains(err.Error(), tt.errSubstr) {
				t.Fatalf("expected error containing %q, got %v", tt.errSubstr, err)
			}
		})
	}
}

// TestExtraClaims_StringsNilVsEmpty pins the distinction the doc comment
// draws: an absent claim or a wrong shape returns nil, while an empty array or
// a blank string returns an empty non-nil slice.
func TestExtraClaims_StringsNilVsEmpty(t *testing.T) {
	if got := (ExtraClaims{"g": []any{}}).Strings("g"); got == nil || len(got) != 0 {
		t.Fatalf("empty array: got %#v, want empty non-nil slice", got)
	}
	if got := (ExtraClaims{"g": "   "}).Strings("g"); got == nil || len(got) != 0 {
		t.Fatalf("blank string: got %#v, want empty non-nil slice", got)
	}
	if got := (ExtraClaims{}).Strings("g"); got != nil {
		t.Fatalf("absent: got %#v, want nil", got)
	}
	if got := (ExtraClaims{"g": 42.0}).Strings("g"); got != nil {
		t.Fatalf("wrong shape: got %#v, want nil", got)
	}
}

func TestUserClaims_Optional(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithUserClaims(ClaimSpec{Optional: []string{"email"}}))
	defer cleanup()

	t.Run("present is extracted", func(t *testing.T) {
		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["email"] = testEmail
		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if got := p.UserPrincipal.ExtraClaims.String("email"); got != testEmail {
			t.Fatalf("email = %q, want %q", got, testEmail)
		}
	})

	t.Run("absent is skipped, no error", func(t *testing.T) {
		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if got := p.UserPrincipal.ExtraClaims.String("email"); got != "" {
			t.Fatalf("expected no email, got %q", got)
		}
	})

	t.Run("empty string is treated as absent", func(t *testing.T) {
		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["email"] = "   "
		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if got := p.UserPrincipal.ExtraClaims.String("email"); got != "" {
			t.Fatalf("expected no email, got %q", got)
		}
	})
}

func TestUserClaims_Required(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithUserClaims(ClaimSpec{Required: []string{"email"}}))
	defer cleanup()

	t.Run("present succeeds", func(t *testing.T) {
		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["email"] = testEmail
		if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims)); err != nil {
			t.Fatalf("extract: %v", err)
		}
	})

	t.Run("absent rejects the token", func(t *testing.T) {
		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		_, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err == nil || !strings.Contains(err.Error(), `missing or empty required claim "email"`) {
			t.Fatalf("expected required-claim error, got %v", err)
		}
	})

	t.Run("empty value rejects the token", func(t *testing.T) {
		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["email"] = ""
		if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims)); err == nil {
			t.Fatalf("expected error for empty required claim")
		}
	})
}

// TestRequiredClaims_RejectUnusableShapes covers the gap the old
// presence-only gate left: a "required" claim carrying a number, bool, object,
// empty array or mixed array satisfied the check and then read back as "" via
// String, silently defeating the consumer's own assertion. The pre-generalized
// code rejected these because the fields were typed *string.
func TestRequiredClaims_RejectUnusableShapes(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithUserClaims(ClaimSpec{Required: []string{"ouId"}}))
	defer cleanup()

	rejected := map[string]any{
		"number":              42,
		"bool":                true,
		"zero":                0,
		"empty array":         []any{},
		"empty object":        map[string]any{},
		"mixed array":         []any{"a", 1},
		"array of blanks":     []any{"  "},
		"array with a blank":  []any{"a", ""},
		"nested object value": map[string]any{"value": "OU-1"},
	}
	for name, value := range rejected {
		t.Run(name, func(t *testing.T) {
			claims := newBaseClaims(AuthorizationCodeGrant)
			claims["sub"] = testUserID
			claims["ouId"] = value
			_, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
			if err == nil {
				t.Fatalf("expected required claim %v (%T) to be rejected", value, value)
			}
			if strings.Contains(err.Error(), "OU-1") {
				t.Fatalf("claim value leaked into the error: %v", err)
			}
		})
	}

	t.Run("non-empty string array is accepted", func(t *testing.T) {
		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["ouId"] = []any{"OU-1", "OU-2"}
		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if got := p.UserPrincipal.ExtraClaims.Strings("ouId"); len(got) != 2 || got[0] != "OU-1" {
			t.Fatalf("ouId = %#v, want [OU-1 OU-2]", got)
		}
	})
}

// TestRequiredClaims_FailureIsDeterministic pins extractDeclared's sorted
// walk. Ranging a map directly would report an arbitrary one of several unmet
// required claims, making the startup/rejection message vary run to run.
func TestRequiredClaims_FailureIsDeterministic(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractorWithOptions(t,
		WithUserClaims(ClaimSpec{Required: []string{"zulu", "alpha", "mike"}}))
	defer cleanup()

	claims := newBaseClaims(AuthorizationCodeGrant)
	claims["sub"] = testUserID // none of the three required claims are present

	var first string
	for i := range 8 {
		_, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err == nil {
			t.Fatalf("expected required-claim error")
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("rejection message is not deterministic:\n %q\n %q", first, err.Error())
		}
	}
	if !strings.Contains(first, `"alpha"`) {
		t.Fatalf("expected the first name in sorted order, got %q", first)
	}
}

// TestClaims_JSONNullIsAbsent pins the documented contract for a JSON-null
// claim: unlike an empty array or false, null means "the IdP said nothing
// here", so an optional declaration skips it and a required one rejects.
func TestClaims_JSONNullIsAbsent(t *testing.T) {
	t.Run("optional null is skipped", func(t *testing.T) {
		extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithUserClaims(ClaimSpec{Optional: []string{"email"}}))
		defer cleanup()

		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["email"] = nil

		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if _, present := p.UserPrincipal.ExtraClaims["email"]; present {
			t.Fatalf("expected a null claim to be skipped, got %#v", p.UserPrincipal.ExtraClaims)
		}
	})

	t.Run("required null rejects the token", func(t *testing.T) {
		extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithUserClaims(ClaimSpec{Required: []string{"email"}}))
		defer cleanup()

		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["email"] = nil

		_, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err == nil || !strings.Contains(err.Error(), `missing or empty required claim "email"`) {
			t.Fatalf("expected required-claim error, got %v", err)
		}
	})
}

// TestOptionalClaims_PreserveNonStringShapes is the counterpart to
// TestRequiredClaims_RejectUnusableShapes: optional declarations stay
// deliberately permissive, and the value reaches the consumer exactly as
// decoded so it can index the map directly for shapes String/Strings do not
// cover. It also pins that normalization is shallow — nested strings are not
// rewritten.
func TestOptionalClaims_PreserveNonStringShapes(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractorWithOptions(t,
		WithUserClaims(ClaimSpec{Optional: []string{"count", "flag", "meta", "mixed", "empty"}}))
	defer cleanup()

	claims := newBaseClaims(AuthorizationCodeGrant)
	claims["sub"] = testUserID
	claims["count"] = 42
	claims["flag"] = true
	claims["meta"] = map[string]any{"nested": "  padded  "}
	claims["mixed"] = []any{"a", 1}
	claims["empty"] = []any{}

	p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	extra := p.UserPrincipal.ExtraClaims

	if got := extra["count"]; got != 42.0 {
		t.Errorf("count = %#v, want 42.0", got)
	}
	if got := extra["flag"]; got != true {
		t.Errorf("flag = %#v, want true", got)
	}
	meta, ok := extra["meta"].(map[string]any)
	if !ok || meta["nested"] != "  padded  " {
		t.Errorf("nested strings must not be rewritten, got %#v", extra["meta"])
	}
	mixed, ok := extra["mixed"].([]any)
	if !ok || len(mixed) != 2 || mixed[0] != "a" || mixed[1] != 1.0 {
		t.Errorf("a mixed array must be left as decoded, got %#v", extra["mixed"])
	}
	// An empty array is "present" (only null and blank strings are absent).
	if _, present := extra["empty"]; !present {
		t.Errorf("an empty array should count as present, got %#v", extra)
	}
}

// TestExtraClaims_ValuesAreTrimmed pins the normalization: emptiness was
// already judged on the trimmed string, so storing the untrimmed one meant a
// padded value reached UserProfileService and any equality comparison there.
func TestExtraClaims_ValuesAreTrimmed(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractorWithOptions(t,
		WithUserClaims(ClaimSpec{Optional: []string{"email", "groups"}, Required: []string{"ouId"}}))
	defer cleanup()

	claims := newBaseClaims(AuthorizationCodeGrant)
	claims["sub"] = testUserID
	claims["email"] = "  trader@example.com \n"
	claims["ouId"] = "  OU-001  "
	claims["groups"] = []any{" a ", "b "}

	p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	extra := p.UserPrincipal.ExtraClaims
	if got := extra.String("email"); got != testEmail {
		t.Fatalf("email = %q, want %q", got, testEmail)
	}
	if got := extra.String("ouId"); got != testOUID {
		t.Fatalf("ouId = %q, want %q", got, testOUID)
	}
	if got := extra.Strings("groups"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("groups = %#v, want [a b]", got)
	}
}

func TestClaims_RequiredWinsRegardlessOfDeclarationOrder(t *testing.T) {
	orders := []struct {
		name string
		opts []Option
	}{
		{"required then optional", []Option{
			WithUserClaims(ClaimSpec{Required: []string{"email"}}),
			WithUserClaims(ClaimSpec{Optional: []string{"email"}}),
		}},
		{"optional then required", []Option{
			WithUserClaims(ClaimSpec{Optional: []string{"email"}}),
			WithUserClaims(ClaimSpec{Required: []string{"email"}}),
		}},
		{"both in one spec", []Option{
			WithUserClaims(ClaimSpec{Optional: []string{"email"}, Required: []string{"email"}}),
		}},
	}
	for _, tt := range orders {
		t.Run(tt.name, func(t *testing.T) {
			extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, tt.opts...)
			defer cleanup()

			claims := newBaseClaims(AuthorizationCodeGrant)
			claims["sub"] = testUserID
			if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims)); err == nil {
				t.Fatalf("expected required-claim error regardless of declaration order")
			}
		})
	}
}

func TestClaims_UserAndClientDeclarationsAreIsolated(t *testing.T) {
	t.Run("user-scoped required claim does not reject client tokens", func(t *testing.T) {
		extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithUserClaims(ClaimSpec{Required: []string{"employee_id"}}))
		defer cleanup()

		claims := newBaseClaims(ClientCredentialsGrant)
		claims["sub"] = "SOME_CLIENT"
		if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims)); err != nil {
			t.Fatalf("expected client token to pass despite unmet user-scoped required claim: %v", err)
		}
	})

	t.Run("client-scoped required claim does not reject user tokens", func(t *testing.T) {
		extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithClientClaims(ClaimSpec{Required: []string{"cost_center"}}))
		defer cleanup()

		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims)); err != nil {
			t.Fatalf("expected user token to pass despite unmet client-scoped required claim: %v", err)
		}
	})
}

func TestClientClaims_Optional(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithClientClaims(ClaimSpec{Optional: []string{"department"}}))
	defer cleanup()

	claims := newBaseClaims(ClientCredentialsGrant)
	claims["sub"] = "SOME_CLIENT"
	claims["department"] = "customs"
	p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got := p.ClientPrincipal.ExtraClaims.String("department"); got != "customs" {
		t.Fatalf("department = %q, want customs", got)
	}
}

func TestClientClaims_Required(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithClientClaims(ClaimSpec{Required: []string{"department"}}))
	defer cleanup()

	claims := newBaseClaims(ClientCredentialsGrant)
	claims["sub"] = "SOME_CLIENT"
	if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims)); err == nil {
		t.Fatalf("expected error for missing required client claim")
	}
}

func TestNewTokenExtractor_RejectsFixedSchemaClaimNames(t *testing.T) {
	// Case variants matter: encoding/json matches struct tags
	// case-insensitively, so "Roles" still lands in tokenClaims.Roles even
	// though a case-sensitive guard would wave it through.
	names := []string{
		"client_id", "grant_type", "scope", "roles", "sub", "iss", "aud", "exp",
		"Roles", "SCOPE", "Client_Id", "SUB",
	}
	for _, name := range names {
		t.Run(name+"/optional", func(t *testing.T) {
			if _, err := NewTokenExtractor("https://localhost/jwks", testIssuer, testClientID, []string{testClientID}, WithUserClaims(ClaimSpec{Optional: []string{name}})); err == nil {
				t.Fatalf("expected error declaring fixed-schema claim %q", name)
			}
		})
		t.Run(name+"/required", func(t *testing.T) {
			if _, err := NewTokenExtractor("https://localhost/jwks", testIssuer, testClientID, []string{testClientID}, WithUserClaims(ClaimSpec{Required: []string{name}})); err == nil {
				t.Fatalf("expected error declaring fixed-schema claim %q", name)
			}
		})
	}

	t.Run("client variant is also rejected", func(t *testing.T) {
		if _, err := NewTokenExtractor("https://localhost/jwks", testIssuer, testClientID, []string{testClientID}, WithClientClaims(ClaimSpec{Required: []string{"scope"}})); err == nil {
			t.Fatalf("expected error declaring fixed-schema claim via client option")
		}
	})

	t.Run("blank name is rejected rather than silently dropped", func(t *testing.T) {
		// The natural result of strings.Split on an env var with a trailing
		// comma. Dropping it silently means an enforcement rule the caller
		// asked for never applies.
		if _, err := NewTokenExtractor("https://localhost/jwks", testIssuer, testClientID, []string{testClientID}, WithUserClaims(ClaimSpec{Required: []string{"email", ""}})); err == nil {
			t.Fatalf("expected error for a blank claim name")
		}
	})

	t.Run("collision error names every offender deterministically", func(t *testing.T) {
		var first string
		for i := range 8 {
			_, err := NewTokenExtractor("https://localhost/jwks", testIssuer, testClientID, []string{testClientID},
				WithUserClaims(ClaimSpec{Optional: []string{"scope", "roles", "sub"}}))
			if err == nil {
				t.Fatalf("expected error")
			}
			if i == 0 {
				first = err.Error()
				continue
			}
			if err.Error() != first {
				t.Fatalf("error message is not deterministic:\n %q\n %q", first, err.Error())
			}
		}
		for _, want := range []string{"scope", "roles", "sub"} {
			if !strings.Contains(first, want) {
				t.Fatalf("error %q does not mention %q", first, want)
			}
		}
	})
}

// TestNewTokenExtractor_AllowsFormerlyFixedClaimNames locks in the schema
// shrink: email/phone_number/ouId/ouHandle used to be hardcoded fixed-schema
// fields and are now legitimate extra-claim declarations.
func TestNewTokenExtractor_AllowsFormerlyFixedClaimNames(t *testing.T) {
	for _, name := range []string{"email", "phone_number", "ouId", "ouHandle", "given_name"} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewTokenExtractor("https://localhost/jwks", testIssuer, testClientID, []string{testClientID}, WithUserClaims(ClaimSpec{Optional: []string{name}})); err != nil {
				t.Fatalf("expected %q to be a legitimate extra claim, got error: %v", name, err)
			}
		})
	}
}
