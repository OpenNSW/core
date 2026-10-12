// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"encoding/json"
	"fmt"
	"strings"
)

// defaultRolesClaim is the claim name authn reads roles from unless a consumer
// points it elsewhere with WithRolesClaim. It must stay in sync with the json
// tag on tokenClaims.Roles.
const defaultRolesClaim = "roles"

// fixedSchemaClaims lists the claims authn binds itself, which are therefore
// kept out of ExtraClaims so each value has exactly one home.
//
// Being in this map means the claim already has a dedicated Principal field or
// is consumed by validation, not that it's inherently more "generic" than a
// claim that isn't. It should
// only grow when a claim actually graduates from "extra" to "fixed" —
// demonstrated load-bearing across multiple independent consumers, the same
// way ouId/ouHandle/email/phone_number were evaluated and found not to
// qualify — never pre-emptively.
//
// Every key MUST be lowercase, because lookups fold the candidate name (see
// foldClaimName). TestFixedSchemaClaims_KeysAreFolded enforces that.
var fixedSchemaClaims = map[string]bool{
	"client_id":       true,
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

// foldClaimName normalizes a claim name for fixed-schema membership checks only.
//
// JWT claim names are case-sensitive (RFC 7519 §4), so this is deliberately
// NOT applied to the names stored in ExtraClaims. Membership checks have to
// fold because the fixed schema is bound by encoding/json, whose struct-tag
// matching IS case-insensitive: a payload key of "Roles" or "Client_Id" lands
// in tokenClaims.Roles / tokenClaims.ClientID just as the lowercase key would.
func foldClaimName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
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
