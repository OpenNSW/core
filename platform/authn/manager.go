package authn

import (
	"net/http"
	"slices"

	coreauthn "github.com/OpenNSW/core/authn"
)

// Manager owns the underlying core/authn manager and exposes the narrow surface
// this application actually uses: one middleware, plus health and shutdown.
type Manager struct {
	core *coreauthn.Manager
}

// NewManager builds the authentication manager. userProfileService is optional;
// pass nil to skip user persistence.
func NewManager(userProfileService UserProfileService, cfg Config) (*Manager, error) {
	// Wrap only a non-nil service: handing core a shim around a nil interface
	// would make it believe persistence is enabled and panic on first login.
	var coreSvc coreauthn.UserProfileService
	if userProfileService != nil {
		coreSvc = coreProfileShim{svc: userProfileService}
	}

	core, err := coreauthn.NewManager(coreSvc, cfg.coreConfig())
	if err != nil {
		return nil, err
	}
	return &Manager{core: core}, nil
}

// RequireAuthMiddleware rejects unauthenticated requests with 401 and attaches
// the caller's Principal, which handlers read with FromContext.
//
// It composes core/authn's middleware with a converter: core validates the
// token and builds its own request context, then attachPrincipal maps that onto
// a Principal under this package's context key. The conversion runs once per
// request, and callers never see core's context type — which is what keeps this
// package the only one that has to track its shape.
func (m *Manager) RequireAuthMiddleware() func(http.Handler) http.Handler {
	requireAuth := m.core.RequireAuthMiddleware()
	return func(next http.Handler) http.Handler {
		return requireAuth(attachPrincipal(next))
	}
}

// attachPrincipal converts core/authn's request context into a Principal. A
// request core deemed authenticated but whose context carries neither a user nor
// a client principal is passed through unattached, so downstream scope and
// ownership checks fail closed.
func attachPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := principalFromAuthContext(coreauthn.GetAuthContext(r.Context())); p != nil {
			// Record the HTTP caller on the outer observer's slot before the
			// child context is built. ContextWithPrincipal does not do this:
			// a later principal must not overwrite who called.
			if slot := RequestCallerFrom(r.Context()); slot != nil {
				slot.Principal = p
			}
			r = r.WithContext(ContextWithPrincipal(r.Context(), p))
		}
		next.ServeHTTP(w, r)
	})
}

// principalFromAuthContext maps core/authn's request context onto a Principal,
// reading the IdP claim names declared in Config.coreConfig.
func principalFromAuthContext(ac *coreauthn.AuthContext) *Principal {
	if ac == nil {
		return nil
	}
	switch {
	case ac.User != nil:
		u := ac.User
		return &Principal{
			Kind:        KindUser,
			UserID:      u.ID,
			IDPUserID:   u.IDPUserID,
			Roles:       u.Roles,
			Scopes:      u.Scopes,
			Email:       u.ExtraClaims.String(claimEmail),
			PhoneNumber: u.ExtraClaims.String(claimPhoneNumber),
			OUID:        u.ExtraClaims.String(claimOUID),
			OUHandle:    u.ExtraClaims.String(claimOUHandle),
			ExtraClaims: extraClaims(u.ExtraClaims, builtinUserClaimNames),
		}
	case ac.Client != nil:
		c := ac.Client
		return &Principal{
			Kind:        KindClient,
			ClientID:    c.ClientID,
			Roles:       c.Roles,
			Scopes:      c.Scopes,
			ExtraClaims: extraClaims(c.ExtraClaims, nil),
		}
	default:
		return nil
	}
}

// extraClaims copies the claims core/authn extracted, minus the built-in names
// already flattened onto Principal fields. The map is copied because core shares
// its own with the profile-service shim and the rest of the request, so a caller
// adding or removing a claim would change what they read; the values are shared,
// which is why Claims is read-only. Returns nil when nothing is left.
func extraClaims(src coreauthn.ExtraClaims, builtin []string) Claims {
	var out Claims
	for name, value := range src {
		if slices.Contains(builtin, name) {
			continue
		}
		if out == nil {
			out = make(Claims, len(src))
		}
		out[name] = value
	}
	return out
}

// Health reports whether the authentication subsystem is usable.
func (m *Manager) Health() error { return m.core.Health() }

// Close releases the manager's resources.
func (m *Manager) Close() error { return m.core.Close() }
