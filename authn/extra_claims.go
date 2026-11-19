// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ExtraClaims holds every claim in a validated token outside authn's fixed
// schema (sub, client_id, scope, the roles claim, and the JWT registered
// claims), which already have dedicated Principal fields or are consumed by
// validation. Nothing has to be declared: whatever the IdP puts in the token
// is here. Deciding which of these a request needs is the application's job.
//
// Values are the claim's JSON-decoded Go representation (string, []any,
// float64, bool, map[string]any), with one normalization: every JSON string a
// value directly carries is whitespace-trimmed — the value itself when it is a
// string, and the elements of a top-level array of strings. Nested objects are
// never rewritten, so what you index out of a map-valued claim is byte-for-byte
// what the IdP sent. Note the fixed-schema claims (sub, client_id, and the
// registered claims) are decoded by jwt.ParseWithClaims and are NOT trimmed.
//
// Use String/Strings for the common shapes; index the map directly to read
// anything else. A nil or empty ExtraClaims is safe to read from and safe to
// call methods on, but is not writable — copy it before mutating.
type ExtraClaims map[string]any

// String returns the claim's value as a string, or "" if the claim is absent
// or not a JSON string (e.g. "email", "ouId", "ouHandle", "given_name").
func (c ExtraClaims) String(name string) string {
	v, _ := c[name].(string)
	return v
}

// Strings returns the claim's value as a string slice. It accepts a JSON array
// of strings, or a single space-delimited string — mirroring how the OAuth2
// "scope" claim is parsed by spaceDelimitedScope in token_parser.go. An empty
// array, or an empty/whitespace-only string, returns an empty (non-nil) slice;
// an absent claim or any other shape (including an array with a non-string
// element) returns nil.
//
// The space-splitting makes this the wrong accessor for a free-text claim:
// Strings("department") on "Customs Division" returns two elements. Use String
// for anything that is not list-shaped.
func (c ExtraClaims) Strings(name string) []string {
	switch v := c[name].(type) {
	case string:
		return strings.Fields(v)
	case []string:
		// Not produced by JSON decoding, but hand-built ExtraClaims literals
		// (tests, consumers assembling a principal) reach this arm and would
		// otherwise silently return nil.
		return slices.Clone(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, s)
		}
		return out
	default:
		return nil
	}
}

// decodeJWTPayload base64url-decodes a compact JWT's payload (second) segment
// into a generic map. It performs NO signature verification: it is only ever
// called from ExtractPrincipalFromHeader after jwt.ParseWithClaims has already
// verified the token, so this is a cheap second decode of already-trusted
// bytes, not an additional crypto pass or trust boundary. It uses the same
// segment split and the same non-strict base64.RawURLEncoding golang-jwt uses,
// so the two decodes cannot disagree about the payload.
func decodeJWTPayload(tokenString string) (map[string]any, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed jwt: expected 3 segments, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode jwt payload segment: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("failed to unmarshal jwt payload: %w", err)
	}
	return claims, nil
}

// claimAbsent reports whether a decoded claim value carries nothing at all:
// JSON null, or a string that is empty once trimmed.
//
// Everything else counts as present — including an empty array, false, and 0 —
// so this is a test for "the IdP said nothing here", not a test for
// usefulness.
func claimAbsent(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	default:
		return false
	}
}

// normalizeClaimValue trims the whitespace around every JSON string a claim
// value directly carries. Any other shape is returned unchanged, and nested
// objects are never rewritten.
func normalizeClaimValue(value any) any {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		// []any, NOT []string: ExtraClaims.Strings switches on the decoded
		// JSON shapes, and handing it a []string would change what an
		// already-validated claim reads back as.
		trimmed := make([]any, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return value // not an array of strings: leave exactly as decoded
			}
			trimmed[i] = strings.TrimSpace(s)
		}
		return trimmed
	default:
		return value
	}
}

// extractExtraClaims returns every claim in an already-decoded payload that is
// not part of the fixed schema, normalized. Absent-valued claims (JSON null, or
// a blank string) are dropped, so they read the same as claims the token never
// carried.
//
// The fixed-schema exclusion folds case: encoding/json binds struct tags
// case-insensitively, so a payload key of "Roles" or "SUB" already landed in a
// fixed field and must not surface a second time under ExtraClaims.
func extractExtraClaims(payload map[string]any) ExtraClaims {
	var extra ExtraClaims
	for name, value := range payload {
		if fixedSchemaClaims[foldClaimName(name)] || claimAbsent(value) {
			continue
		}
		if extra == nil {
			extra = make(ExtraClaims)
		}
		extra[name] = normalizeClaimValue(value)
	}
	return extra
}

// resolveClaims produces the roles slice and the extra claims of a principal.
// The extra claims need a second decode of the payload, since tokenClaims only
// binds the fixed schema.
func resolveClaims(tokenString string, claims *tokenClaims) (ExtraClaims, []string, error) {
	roles, err := parseRolesClaim(claims.Roles.value)
	if err != nil {
		return nil, nil, err
	}

	payload, err := decodeJWTPayload(tokenString)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode token payload for claim extraction: %w", err)
	}

	return extractExtraClaims(payload), roles, nil
}
