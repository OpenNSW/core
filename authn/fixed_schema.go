// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// defaultRolesClaim is the claim name authn reads roles from unless a consumer
// points it elsewhere with WithRolesClaim. It must stay in sync with the json
// tag on tokenClaims.Roles.
const defaultRolesClaim = "roles"

// fixedSchemaClaims guards against a consumer redeclaring a claim name that is
// already part of authn's fixed schema as an extra claim.
//
// This is purely a mechanical name-collision guard, not a "standard claims"
// allowlist: being in this map means the claim already has a dedicated field
// (or, for sub/client_id/grant_type, drives the package's own dispatch logic),
// not that it's inherently more "generic" than a claim that isn't. It should
// only grow when a claim actually graduates from "extra" to "fixed" —
// demonstrated load-bearing across multiple independent consumers, the same
// way ouId/ouHandle/email/phone_number were evaluated and found not to
// qualify — never pre-emptively.
//
// Every key MUST be lowercase, because lookups fold the candidate name (see
// foldClaimName). TestFixedSchemaClaims_KeysAreFolded enforces that.
var fixedSchemaClaims = map[string]bool{
	"client_id":       true,
	"grant_type":      true,
	"scope":           true,
	defaultRolesClaim: true,
	// JWT registered claims (RFC 7519 §4.1), always present via jwt.RegisteredClaims.
	"iss": true,
	"sub": true,
	"aud": true,
	"exp": true,
	"nbf": true,
	"iat": true,
	"jti": true,
}

// foldClaimName normalizes a claim name for fixed-schema collision checks only.
//
// JWT claim names are case-sensitive (RFC 7519 §4) and extraction matches them
// exactly, so this is deliberately NOT applied when reading a payload. The
// collision guard has to fold because the fixed schema is bound by
// encoding/json, whose struct-tag matching IS case-insensitive: without
// folding, declaring "Roles" or "Client_Id" passes the guard while the very
// same payload key still lands in tokenClaims.Roles / tokenClaims.ClientID.
func foldClaimName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// validateClaimNames reports every problem with one set of declared extra-claim
// names at once — blank names, and names already bound by authn — so a
// consumer fixes all of them in one pass instead of one per restart. what names
// the declaration site (e.g. "UserClaims.Required") and appears in the error.
//
// rolesClaim is reserved alongside the fixed schema: once roles are read from
// "groups", that name has a dedicated field (Principal.Roles) and declaring it
// as an extra claim would surface one claim under two meanings.
func validateClaimNames(what string, names []string, rolesClaim string) error {
	var blank bool
	var reserved []string

	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			blank = true
			continue
		}
		folded := foldClaimName(trimmed)
		if fixedSchemaClaims[folded] || folded == foldClaimName(rolesClaim) {
			reserved = append(reserved, trimmed)
		}
	}

	if blank {
		return fmt.Errorf("%s contains an empty claim name", what)
	}
	if len(reserved) > 0 {
		// Sorted and de-duplicated so the message is identical run to run;
		// callers hit this at startup and may assert on it in tests.
		slices.Sort(reserved)
		reserved = slices.Compact(reserved)
		return fmt.Errorf(
			"%s declares claim(s) %v that authn already binds (fixed schema: client_id, grant_type, scope, %s, and the JWT registered claims)",
			what, reserved, rolesClaim,
		)
	}
	return nil
}

// validateRolesClaimName reports whether name is usable as the roles claim.
// Any case-spelling of "roles" is fine — it IS the roles claim. Any other
// fixed-schema name is not: it already feeds a different field, and pointing
// roles at it would bind one claim to two meanings (WithRolesClaim("scope")
// would try to read the space-delimited scope string as a roles array and
// reject every token).
func validateRolesClaimName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("roles claim name is empty")
	}
	if folded := foldClaimName(trimmed); folded != defaultRolesClaim && fixedSchemaClaims[folded] {
		return fmt.Errorf("roles claim %q is part of authn's fixed schema and already feeds a different field", trimmed)
	}
	return nil
}

// rolesValue captures the raw "roles" claim without imposing a shape at JSON
// decode time.
//
// Binding the field as []string would make the SHAPE of the literal "roles"
// claim a hard token-validity condition even for consumers who moved roles
// elsewhere with WithRolesClaim: struct tags are static, so a token carrying
// `"roles": "legacy-string"` would be rejected by encoding/json before authn
// ever looked at the configured claim. Capturing the raw value defers that
// decision to parseRolesClaim, which applies it only to the claim in force.
type rolesValue struct{ value any }

func (r *rolesValue) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &r.value)
}

// parseRolesClaim converts an already-decoded roles claim value into the roles
// slice, enforcing exactly the shape a []string field would: absent or JSON
// null yields no roles; an array of strings yields those roles (an empty array
// yields an empty, non-nil slice); anything else rejects the token. Role
// strings are whitespace-trimmed, since a role of " Trader " can never match a
// check for "Trader".
//
// Note this is deliberately stricter than ExtraClaims.Strings, which also
// accepts a single space-delimited string. That convention is right for the
// OAuth2 "scope" claim it was built for, but for roles it would turn
// `"roles": "Customs Officer"` into two roles and let a check for "Customs"
// pass for a principal that was never granted it.
//
// Rejecting rather than ignoring a wrong-shaped value is also deliberate: roles
// feed authz.Principal, and a silently empty roles slice reads as "granted
// nothing" — which fails closed for a require-role check, but fails OPEN for
// any check phrased as "deny if the principal has role X". A mistyped
// WithRolesClaim must fail loudly on the first request.
func parseRolesClaim(name string, value any) ([]string, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case []any:
		roles := make([]string, 0, len(v))
		for _, item := range v {
			role, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("jwt claim %q must be an array of strings", name)
			}
			roles = append(roles, strings.TrimSpace(role))
		}
		return roles, nil
	default:
		return nil, fmt.Errorf("jwt claim %q must be an array of strings", name)
	}
}
