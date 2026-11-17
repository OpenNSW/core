package authn

import "context"

// ScopePrincipal exposes a Principal through the narrow Subject/Roles/Scopes
// accessor trio that core/authz's Principal interface requires.
//
// It satisfies that interface structurally, which is why this package imports
// nothing from core/authz and the composition root keeps owning the
// authorization wiring: bootstrap builds the authz Authorizer but sources its
// principal from here, so core/authn stays confined to this package.
type ScopePrincipal struct {
	p *Principal
}

func (s ScopePrincipal) Subject() string { return s.p.Subject() }

func (s ScopePrincipal) Roles() []string {
	if s.p == nil {
		return nil
	}
	return s.p.Roles
}

func (s ScopePrincipal) Scopes() []string {
	if s.p == nil {
		return nil
	}
	return s.p.Scopes
}

// ScopePrincipalFromContext adapts the request's Principal for core/authz's
// extractor. ok is false for an unauthenticated request, which the authorizer
// translates into a 401.
func ScopePrincipalFromContext(ctx context.Context) (ScopePrincipal, bool) {
	p, ok := FromContext(ctx)
	if !ok {
		return ScopePrincipal{}, false
	}
	return ScopePrincipal{p: p}, true
}
