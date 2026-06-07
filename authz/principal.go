package authz

import (
	"context"
	"errors"
	"slices"
)

// Principal is the minimal, transport-agnostic identity contract this package
// needs to make authorization decisions. It is deliberately tiny so an
// authentication layer's context type satisfies it *structurally* — in this
// codebase *auth.AuthContext already implements Subject/Roles/Scopes — which is
// why this package imports nothing from the authn layer. Authorization is driven
// by the OAuth2 scopes granted on the token; Roles is exposed for callers that
// need finer, business-level checks at the service layer.
type Principal interface {
	// Subject returns a stable identifier for the principal: a user ID for user
	// principals or a machine client ID for client (M2M) principals.
	Subject() string
	// Roles returns the roles granted to the principal, or nil.
	Roles() []string
	// Scopes returns the OAuth2 scopes granted to the principal, or nil.
	Scopes() []string
}

// Extractor retrieves the authenticated Principal from a request context. It is
// injected at construction so this package stays decoupled from any specific
// authentication implementation. It must return (nil, false) when the request is
// unauthenticated.
type Extractor func(ctx context.Context) (Principal, bool)

// Sentinel errors for callers performing service-layer authorization. The HTTP
// middleware in this package translates these conditions into 401/403 itself.
var (
	ErrUnauthenticated = errors.New("authz: unauthenticated")
	ErrForbidden       = errors.New("authz: forbidden")
)

// HasScope reports whether the principal was granted the exact scope.
func HasScope(p Principal, scope string) bool {
	if p == nil || scope == "" {
		return false
	}
	return slices.Contains(p.Scopes(), scope)
}

// HasAnyScope reports whether the principal holds at least one of the scopes.
// With no scopes provided it returns false.
func HasAnyScope(p Principal, scopes ...string) bool {
	for _, s := range scopes {
		if HasScope(p, s) {
			return true
		}
	}
	return false
}

// HasAllScopes reports whether the principal holds every one of the scopes.
// With no scopes provided it returns true (vacuously satisfied).
func HasAllScopes(p Principal, scopes ...string) bool {
	for _, s := range scopes {
		if !HasScope(p, s) {
			return false
		}
	}
	return true
}
