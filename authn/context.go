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
// Example implementation:
//
//	type MyUserService struct {
//	    db *sql.DB
//	}
//
//	func (s *MyUserService) GetOrCreateUser(userID, email, phone, organizationID, ouHandle string) (*string, error) {
//	    // Your implementation to create or fetch the user idempotently
//	    persistedID := "generated-id"
//	    if err := s.db.Exec("INSERT INTO users ...", userID, email, phone, organizationID).Error; err != nil {
//	        return nil, err
//	    }
//	    return &persistedID, nil
//	}
//
//	authManager := auth.NewManager(myUserService, cfg.Auth)  // myUserService can be nil
type UserProfileService interface {
	// GetOrCreateUser creates or retrieves a user profile.
	// Parameters:
	//   - idpUserID: the unique user ID from the identity provider (required)
	//   - email: user's email address (required)
	//   - phone: user's phone number (can be empty)
	//   - organizationID: organization/tenant identifier (required)
	//   - ouHandle: organization unit handle from the identity provider (required)
	//
	// Implementation notes:
	//   - Should be idempotent: calling multiple times with same idpUserID should be safe
	//   - Called during first login after token validation
	//   - Errors are logged but don't block authentication
	//   - Should not return error if user already exists
	//   - If err is non-nil, the returned user ID should be nil
	// Returns user ID of the created or existing user, or an error if the operation fails.
	GetOrCreateUser(idpUserID, email, phone, organizationID, ouHandle string) (*string, error)
}

// UserContext represents a user principal's runtime context injected into each request.
// It includes identity fields and principal-derived roles.
// Note: Per-request NSWData is not persisted here; services requiring user metadata
// should call the user profile service on-demand.
type UserContext struct {
	ID          string   `json:"id"`
	IDPUserID   string   `json:"idpUserId"`
	Email       string   `json:"email"`
	PhoneNumber string   `json:"phoneNumber"`
	OUID        string   `json:"ouId"`
	OUHandle    string   `json:"ouHandle"`
	Roles       []string `json:"roles"`
	Scopes      []string `json:"scopes"`
}

// ClientContext represents a machine client's context.
type ClientContext struct {
	ClientID string   `json:"clientId"`
	Roles    []string `json:"roles"`
	Scopes   []string `json:"scopes"`
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
