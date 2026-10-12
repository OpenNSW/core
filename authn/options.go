// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

// Option configures optional behavior on TokenExtractor construction.
//
// Options never fail on their own: they record intent verbatim and every check
// runs afterwards in validateConfig, which NewTokenExtractor reports as a
// construction error.
type Option func(*TokenExtractor)

// WithRolesClaim points authn at a differently-named claim for the roles that
// populate Principal.Roles and AuthContext.Roles(), for identity providers
// that do not emit a top-level "roles" claim (Okta commonly uses "groups").
// Defaults to "roles".
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
// The configured claim feeds Roles only: like the rest of the fixed schema,
// it is never repeated in ExtraClaims.
func WithRolesClaim(name string) Option {
	// Recorded verbatim; validateRolesClaimName judges it in validateConfig.
	return func(te *TokenExtractor) { te.rolesClaim = name }
}
