// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import "strings"

// Option configures optional behavior on TokenExtractor construction.
//
// Options never fail on their own: they record intent verbatim and every check
// runs afterwards in validateConfig, so NewTokenExtractor reports a bad
// declaration as a construction error regardless of the order options were
// passed in.
type Option func(*TokenExtractor)

// ClaimSpec declares the extra JWT claims — those beyond authn's fixed schema
// (client_id, grant_type, scope, roles, and the JWT registered claims) — to
// extract for one principal type.
//
// Optional claims are best-effort: a name that is absent, JSON-null, or an
// empty/whitespace-only string is silently skipped and never fails a token.
// Required claims must carry a usable value — a non-blank string, or a
// non-empty array of non-blank strings — or ExtractPrincipalFromHeader rejects
// the token. A name listed in both is required.
//
// Names are matched exactly against the token payload: JWT claim names are
// case-sensitive (RFC 7519 §4). Nested lookups are not supported; a name is a
// top-level key, which is what makes namespaced names like
// "https://app.example.com/roles" work unchanged.
type ClaimSpec struct {
	Optional []string
	Required []string
}

func (s ClaimSpec) isZero() bool {
	return len(s.Optional) == 0 && len(s.Required) == 0
}

// WithUserClaims declares extra claims to extract from user-principal
// (authorization_code grant) tokens. Extracted values surface on
// UserPrincipal.ExtraClaims / UserContext.ExtraClaims.
//
// Repeated calls accumulate rather than replace, so a name declared required by
// any call stays required.
func WithUserClaims(spec ClaimSpec) Option {
	return func(te *TokenExtractor) { te.userClaims.merge(spec) }
}

// WithClientClaims is the WithUserClaims analogue for client-credential (M2M)
// tokens, populating ClientPrincipal.ExtraClaims / ClientContext.ExtraClaims.
//
// The declaration is separate because the two token types carry different
// claim sets: a client token may carry e.g. "department" or "cost_center",
// while "email"/"ouHandle"/"given_name" only appear on user tokens. A name
// declared for one principal type is never extracted from the other.
func WithClientClaims(spec ClaimSpec) Option {
	return func(te *TokenExtractor) { te.clientClaims.merge(spec) }
}

// WithRolesClaim points authn at a differently-named claim for the roles that
// populate Principal.Roles and AuthContext.Roles(), for identity providers
// that do not emit a top-level "roles" claim (Okta commonly uses "groups").
// Defaults to "roles". The name applies to both user and client principals.
//
// The claim must be a top-level JSON array of strings — exactly the shape the
// default "roles" claim must have. An absent claim yields no roles; a present
// claim of any other shape rejects the token, so a mistyped name fails loudly
// on the first request instead of silently disabling every role check.
//
// Dotted paths into nested objects (Keycloak's realm_access.roles) are NOT
// supported: names are matched exactly, which is what keeps namespaced claim
// names such as "https://app.example.com/roles" working. Flatten a nested
// roles claim with an IdP-side protocol mapper.
//
// The configured name is reserved: it cannot also be declared as an extra
// claim via WithUserClaims/WithClientClaims, since it already has a dedicated
// field.
func WithRolesClaim(name string) Option {
	// Recorded verbatim; validateRolesClaimName judges it in validateConfig,
	// so this behaves identically whether it runs before or after the
	// WithUserClaims/WithClientClaims calls it interacts with.
	return func(te *TokenExtractor) { te.rolesClaim = name }
}

func (s *ClaimSpec) merge(other ClaimSpec) {
	s.Optional = append(s.Optional, other.Optional...)
	s.Required = append(s.Required, other.Required...)
}

// resolve flattens a declaration into the name -> required map the extraction
// path uses. Required is applied after Optional, so a name appearing in both
// (in either slice, from any number of merged calls) ends up required.
// Returns nil for an empty spec, which is what lets extraction skip its
// payload decode entirely for consumers who never opted in.
func (s ClaimSpec) resolve() map[string]bool {
	if s.isZero() {
		return nil
	}
	declared := make(map[string]bool, len(s.Optional)+len(s.Required))
	for _, name := range s.Optional {
		declared[strings.TrimSpace(name)] = false
	}
	for _, name := range s.Required {
		declared[strings.TrimSpace(name)] = true
	}
	return declared
}
