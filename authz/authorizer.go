package authz

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// Authorizer is the policy enforcement point. It resolves the authenticated
// Principal (via the injected Extractor) and provides middleware that gates HTTP
// routes on OAuth2 scopes.
type Authorizer struct {
	extract Extractor
}

// New constructs an Authorizer. extract supplies the authenticated Principal from
// a request context (typically wrapping the authn layer's context lookup). It
// returns an error if extract is nil.
func New(extract Extractor) (*Authorizer, error) {
	if extract == nil {
		return nil, errors.New("authz: New requires a non-nil Extractor")
	}
	return &Authorizer{extract: extract}, nil
}

// Principal returns the authenticated principal from ctx, or (nil, false) when
// the request is unauthenticated. Useful for service/handler-layer checks that go
// beyond a coarse scope gate.
func (a *Authorizer) Principal(ctx context.Context) (Principal, bool) {
	if a == nil || a.extract == nil {
		return nil, false
	}
	p, ok := a.extract(ctx)
	if !ok || p == nil {
		return nil, false
	}
	return p, true
}

// RequireScope returns middleware that admits a request only if the principal
// holds scope: 401 when unauthenticated, 403 when authenticated but missing it.
func (a *Authorizer) RequireScope(scope string) func(http.Handler) http.Handler {
	if scope == "" {
		panic("authz: RequireScope requires a non-empty scope")
	}
	return a.require(func(p Principal) bool { return HasScope(p, scope) }, scope)
}

// RequireAnyScope admits the request if the principal holds at least one of scopes.
func (a *Authorizer) RequireAnyScope(scopes ...string) func(http.Handler) http.Handler {
	if len(scopes) == 0 {
		panic("authz: RequireAnyScope requires at least one scope")
	}
	for _, s := range scopes {
		if s == "" {
			panic("authz: RequireAnyScope requires non-empty scopes")
		}
	}
	return a.require(func(p Principal) bool { return HasAnyScope(p, scopes...) }, scopes...)
}

// RequireAllScopes admits the request only if the principal holds every one of scopes.
func (a *Authorizer) RequireAllScopes(scopes ...string) func(http.Handler) http.Handler {
	if len(scopes) == 0 {
		panic("authz: RequireAllScopes requires at least one scope")
	}
	for _, s := range scopes {
		if s == "" {
			panic("authz: RequireAllScopes requires non-empty scopes")
		}
	}
	return a.require(func(p Principal) bool { return HasAllScopes(p, scopes...) }, scopes...)
}

func (a *Authorizer) require(allow func(Principal) bool, required ...string) func(http.Handler) http.Handler {
	if a == nil || a.extract == nil {
		panic("authz: middleware constructed on a nil Authorizer")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := a.extract(r.Context())
			if !ok || p == nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
				return
			}
			if !allow(p) {
				slog.Warn("authz: forbidden",
					"subject", p.Subject(),
					"required_scopes", required,
					"granted_scopes", p.Scopes(),
					"method", r.Method,
					"path", r.URL.Path,
				)
				writeError(w, http.StatusForbidden, "forbidden", "insufficient scope")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message})
}
