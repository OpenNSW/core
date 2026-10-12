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

// TestExtraClaims_PassThrough locks in that every non-fixed claim reaches
// ExtraClaims without being declared, in its decoded shape.
func TestExtraClaims_PassThrough(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	claims := newBaseClaims()
	claims["email"] = testEmail
	claims["ouId"] = testOUID
	claims["grant_type"] = "client_credentials"
	claims["https://app.example.com/department"] = "customs"
	claims["count"] = 42
	claims["flag"] = true
	claims["meta"] = map[string]any{"nested": "  padded  "}
	claims["mixed"] = []any{"a", 1}
	claims["empty"] = []any{}

	p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	extra := p.ExtraClaims

	for name, want := range map[string]string{
		"email":                              testEmail,
		"ouId":                               testOUID,
		"grant_type":                         "client_credentials",
		"https://app.example.com/department": "customs",
	} {
		if got := extra.String(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
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

// TestExtraClaims_ExcludesFixedSchema locks in that each value has one home:
// a claim authn binds itself never reappears in ExtraClaims, including case
// variants that encoding/json bound to the fixed field anyway.
func TestExtraClaims_ExcludesFixedSchema(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	claims := newBaseClaims()
	claims["jti"] = "token-1"
	claims["scope"] = "nsw:task:read"
	claims["roles"] = []string{"exporter"}
	claims["email"] = testEmail

	p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(p.ExtraClaims) != 1 || p.ExtraClaims.String("email") != testEmail {
		t.Fatalf("ExtraClaims = %#v, want only email", p.ExtraClaims)
	}

	t.Run("case variants", func(t *testing.T) {
		claims := newBaseClaims()
		claims["Roles"] = []string{"exporter"}
		claims["SCOPE"] = "nsw:task:read"

		p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if len(p.ExtraClaims) != 0 {
			t.Fatalf("ExtraClaims = %#v, want none", p.ExtraClaims)
		}
	})
}

// TestExtraClaims_ExcludesCustomRolesClaim covers WithRolesClaim: the
// configured claim feeds Roles and is excluded exactly as it is read — a
// differently-cased key never fed Roles, so it stays an extra claim.
func TestExtraClaims_ExcludesCustomRolesClaim(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractorWithOptions(t, WithRolesClaim("groups"))
	defer cleanup()

	claims := newBaseClaims()
	claims["groups"] = []string{"exporter"}
	claims["Groups"] = "other"

	p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, present := p.ExtraClaims["groups"]; present {
		t.Fatalf("roles claim leaked into ExtraClaims: %#v", p.ExtraClaims)
	}
	if got := p.ExtraClaims.String("Groups"); got != "other" {
		t.Fatalf("Groups = %q, want other", got)
	}
}

func TestExtraClaims_AbsentValuesAreDropped(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	claims := newBaseClaims()
	claims["email"] = nil
	claims["ouId"] = "   "

	p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if p.ExtraClaims != nil {
		t.Fatalf("ExtraClaims = %#v, want nil", p.ExtraClaims)
	}
}

// TestExtraClaims_ValuesAreTrimmed pins the normalization: a padded value
// would otherwise fail any equality comparison a consumer makes.
func TestExtraClaims_ValuesAreTrimmed(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	claims := newBaseClaims()
	claims["email"] = "  trader@example.com \n"
	claims["groups"] = []any{" a ", "b "}

	p, err := extractor.ExtractPrincipalFromHeader("Bearer " + signToken(t, privateKey, claims))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	extra := p.ExtraClaims
	if got := extra.String("email"); got != testEmail {
		t.Fatalf("email = %q, want %q", got, testEmail)
	}
	if got := extra.Strings("groups"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("groups = %#v, want [a b]", got)
	}
}
