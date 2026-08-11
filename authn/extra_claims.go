// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
)

// ExtraClaims holds JWT claims outside authn's fixed schema, declared
// explicitly by a consumer via WithUserClaims (user principals) or
// WithClientClaims (client principals).
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
// usefulness. Required claims apply requiredClaimValue on top.
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

// requiredClaimValue reports whether value satisfies the shape a REQUIRED claim
// must have, returning it normalized.
//
// The shape is deliberately narrow: a non-blank JSON string, or a non-empty
// array whose every element is a non-blank JSON string. A required declaration
// is a consumer asserting "my application cannot run without this attribute";
// a number, boolean, object, empty array or mixed array cannot satisfy that,
// and accepting one only defers the failure to the code that indexes the claim
// with String/Strings and silently gets "" or nil.
func requiredClaimValue(value any) (any, bool) {
	switch v := value.(type) {
	case string:
		s := strings.TrimSpace(v)
		return s, s != ""
	case []any:
		if len(v) == 0 {
			return nil, false
		}
		out := make([]any, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			if s = strings.TrimSpace(s); s == "" {
				return nil, false
			}
			out[i] = s
		}
		return out, true
	default:
		return nil, false
	}
}

// extractDeclared pulls the claims named in declared (name -> required) out of
// an already-decoded payload. Names are matched exactly: JWT claim names are
// case-sensitive (RFC 7519 §4), unlike the struct-tag binding the fixed schema
// relies on. Names are walked in sorted order, so a token violating two
// required claims always fails on the same one.
func extractDeclared(payload map[string]any, declared map[string]bool) (ExtraClaims, error) {
	// nil rather than an empty map, so "nothing declared" reads the same way
	// whether or not a custom roles claim forced the payload decode.
	if len(declared) == 0 {
		return nil, nil
	}
	extra := make(ExtraClaims, len(declared))
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		required := declared[name]
		value, present := payload[name]

		if !present || claimAbsent(value) {
			if required {
				// Claim NAMES only, never values: these errors reach the
				// middleware's Debug log with request context, and email /
				// phone_number are exactly the claims people mark required.
				return nil, fmt.Errorf("jwt missing or empty required claim %q", name)
			}
			continue
		}

		if required {
			normalized, ok := requiredClaimValue(value)
			if !ok {
				return nil, fmt.Errorf("jwt required claim %q must be a non-empty string or a non-empty array of non-empty strings", name)
			}
			extra[name] = normalized
			continue
		}
		extra[name] = normalizeClaimValue(value)
	}
	return extra, nil
}

// resolveClaims produces the two payload-derived pieces of a principal that
// depend on consumer configuration — the roles slice and the declared extra
// claims — sharing a single payload decode between them.
//
// When nothing is declared and roles come from the default "roles" claim (every
// consumer that has not opted in), there is nothing to read that
// jwt.ParseWithClaims has not already bound, so no second decode happens.
func (te *TokenExtractor) resolveClaims(tokenString string, claims *tokenClaims, declared map[string]bool) (ExtraClaims, []string, error) {
	// The "" guard matters: a TokenExtractor built as a struct literal (only
	// possible in-package) has no rolesClaim and must not read as "custom".
	custom := te.rolesClaim != "" && te.rolesClaim != defaultRolesClaim

	if len(declared) == 0 && !custom {
		roles, err := parseRolesClaim(defaultRolesClaim, claims.Roles.value)
		return nil, roles, err
	}

	payload, err := decodeJWTPayload(tokenString)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode token payload for claim extraction: %w", err)
	}

	rolesName, rolesRaw := defaultRolesClaim, claims.Roles.value
	if custom {
		rolesName, rolesRaw = te.rolesClaim, payload[te.rolesClaim]
		if rolesRaw == nil {
			slog.Debug("configured roles claim absent from token", "claim", rolesName)
		}
	}
	roles, err := parseRolesClaim(rolesName, rolesRaw)
	if err != nil {
		return nil, nil, err
	}

	extra, err := extractDeclared(payload, declared)
	if err != nil {
		return nil, nil, err
	}
	return extra, roles, nil
}
