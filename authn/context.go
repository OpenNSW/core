// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"context"
)

// UserProfileService defines the contract for managing user profiles.
// Implementations are responsible for persisting and managing user records in their system.
//
// This interface is OPTIONAL when using the auth package. If not provided (nil),
// user creation on first login will be skipped. This allows:
//
// 1. Systems that don't track user profiles - just use auth for token validation
// 2. Systems that manage user profiles separately - implement this interface
// 3. Systems that handle user creation elsewhere - pass nil
//
// The whole authenticated principal is passed rather than a list of named
// identity values, because everything except the JWT "sub" claim (email, phone
// number, organization/tenant identifiers, ...) is IdP/consumer-specific.
// Naming those as parameters is what forced this interface to change once
// already; passing the principal means adding a claim never changes the
// signature again.
//
// Example implementation:
//
//	type MyUserService struct {
//	    db *sql.DB
//	}
//
//	func (s *MyUserService) GetOrCreateUser(ctx context.Context, principal *authn.UserPrincipal) (string, error) {
//	    // Your implementation to create or fetch the user idempotently
//	    email := principal.ExtraClaims.String("email")
//	    persistedID := "generated-id"
//	    if err := s.db.Exec("INSERT INTO users ...", principal.Subject, email).Error; err != nil {
//	        return "", err
//	    }
//	    return persistedID, nil
//	}
//
//	authManager := auth.NewManager(myUserService, cfg.Auth)  // myUserService can be nil
type UserProfileService interface {
	// GetOrCreateUser creates or retrieves a user profile.
	//
	// principal is never nil, and MUST NOT be mutated: the middleware has
	// already built the request's AuthContext from it and shares the same
	// ExtraClaims map, so writing to it would alter the live request context.
	// (ExtraClaims is also nil unless claims were declared — nil maps read
	// fine but panic on assignment.)
	//
	//   - principal.Subject is the identity provider's user ID (the JWT "sub"
	//     claim), NOT the persisted ID this method returns.
	//   - principal.ExtraClaims holds the claims declared via WithUserClaims /
	//     Config.UserClaims; nil if none were declared.
	//
	// Implementation notes:
	//   - Should be idempotent: calling multiple times with the same subject should be safe
	//   - Called during first login after token validation
	//   - Errors are logged but don't block authentication
	//   - Should not return error if user already exists
	// Returns user ID of the created or existing user, or an error if the operation fails.
	GetOrCreateUser(ctx context.Context, principal *UserPrincipal) (string, error)
}

// UserContext represents a user principal's runtime context injected into each request.
// It includes identity fields and principal-derived roles.
// Note: Per-request NSWData is not persisted here; services requiring user metadata
// should call the user profile service on-demand.
type UserContext struct {
	ID          string      `json:"id"`
	IDPUserID   string      `json:"idpUserId"`
	Roles       []string    `json:"roles"`
	Scopes      []string    `json:"scopes"`
	ExtraClaims ExtraClaims `json:"extraClaims,omitempty"`
}

// ClientContext represents a machine client's context.
type ClientContext struct {
	ClientID    string      `json:"clientId"`
	Roles       []string    `json:"roles"`
	Scopes      []string    `json:"scopes"`
	ExtraClaims ExtraClaims `json:"extraClaims,omitempty"`
}

// AuthContext is the transient authentication context injected into each request
// by the auth middleware.
// For user principals, User contains identity fields and roles.
// For client principals (M2M), Client is set.
type AuthContext struct {
	User   *UserContext
	Client *ClientContext
}

// ContextKey is a custom type for context keys to avoid collisions.
type ContextKey string

const AuthContextKey ContextKey = "authContext"

// GetAuthContext extracts the AuthContext from a request context.
// Returns nil if no auth context is available (for example: public route,
// missing auth header, or middleware not applied).
//
// Usage in handlers:
//
//	authCtx := auth.GetAuthContext(r.Context())
//	if authCtx == nil {
//	    // Handle unauthorized request
//	}
//	userID := authCtx.User.ID
func GetAuthContext(ctx context.Context) *AuthContext {
	authCtx, ok := ctx.Value(AuthContextKey).(*AuthContext)
	if !ok {
		return nil
	}
	return authCtx
}

// The methods below form a small, stable seam over the authenticated principal so
// that a future authz layer can depend on a narrow interface it defines itself
// (e.g. interface{ Roles() []string; Scopes() []string; Subject() string }) which
// *AuthContext satisfies structurally — rather than reaching into the concrete
// User/Client fields or branching on principal type. They are nil-safe.

// Type reports the principal type of the context: UserPrincipalType,
// ClientPrincipalType, or "" when unauthenticated.
func (a *AuthContext) Type() PrincipalType {
	switch {
	case a == nil:
		return ""
	case a.User != nil:
		return UserPrincipalType
	case a.Client != nil:
		return ClientPrincipalType
	default:
		return ""
	}
}

// Subject returns a stable identifier for the principal: the resolved user ID
// (falling back to the IdP user ID) for users, the client ID for clients, or "".
func (a *AuthContext) Subject() string {
	switch {
	case a == nil:
		return ""
	case a.User != nil:
		if a.User.ID != "" {
			return a.User.ID
		}
		return a.User.IDPUserID
	case a.Client != nil:
		return a.Client.ClientID
	default:
		return ""
	}
}

// Roles returns the granted roles for the principal (user or client), or nil.
func (a *AuthContext) Roles() []string {
	switch {
	case a == nil:
		return nil
	case a.User != nil:
		return a.User.Roles
	case a.Client != nil:
		return a.Client.Roles
	default:
		return nil
	}
}

// Scopes returns the granted OAuth2 scopes for the principal (user or client), or nil.
func (a *AuthContext) Scopes() []string {
	switch {
	case a == nil:
		return nil
	case a.User != nil:
		return a.User.Scopes
	case a.Client != nil:
		return a.Client.Scopes
	default:
		return nil
	}
}

// ExtraClaims returns the consumer-declared extra claims for the principal
// (user or client), or nil. Nil-safe, and ExtraClaims' own methods are nil-safe
// too, so authCtx.ExtraClaims().String("email") never panics.
//
// Like Roles and Scopes, this returns the live value rather than a copy — do
// not mutate it.
func (a *AuthContext) ExtraClaims() ExtraClaims {
	switch {
	case a == nil:
		return nil
	case a.User != nil:
		return a.User.ExtraClaims
	case a.Client != nil:
		return a.Client.ExtraClaims
	default:
		return nil
	}
}
