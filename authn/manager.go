package authn

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Manager handles all authentication-related operations and middleware setup.
// It encapsulates the token extraction and middleware creation,
// providing a clean interface for the HTTP server to use.
//
// This manager pattern keeps auth logic self-contained and optionally delegates user persistence
// to a UserProfileService (if provided). The manager can work without a user profile service,
// making it suitable for any authentication use case.
type Manager struct {
	userProfileService UserProfileService
	tokenExtractor     *TokenExtractor
	middleware         func(http.Handler) http.Handler
}

// NewManager creates and initializes a new auth manager.
// This is the single entry point for all auth initialization in the application.
//
// userProfileService is OPTIONAL. If not provided (nil), user creation on first login is disabled.
// tokenExtractor is REQUIRED and is always initialized by this constructor.
// This allows the auth package to be used in systems that don't track user profiles
// while still guaranteeing auth token parsing is available.
//
// Usage examples:
//
//	// With user profile service (NSW example)
//	userProfileService := user.NewService(db)
//	authManager := auth.NewManager(userProfileService, cfg.Auth)
//
//	// Without user profile service (generic auth only)
//	authManager := auth.NewManager(nil, cfg.Auth)
//
//	// With custom user profile service
//	customService := &MyCustomUserService{}
//	authManager := auth.NewManager(customService, cfg.Auth)
func NewManager(userProfileService UserProfileService, authConfig Config) (*Manager, error) {
	slog.Info("initializing auth manager", "user_profile_service_enabled", userProfileService != nil)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	if authConfig.InsecureSkipTLSVerify {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	tokenExtractor, err := NewTokenExtractorWithClient(
		authConfig.JWKSURL, authConfig.Issuer, authConfig.Audience, authConfig.ClientIDs, httpClient,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize token extractor: %w", err)
	}
	if tokenExtractor == nil {
		return nil, fmt.Errorf("token extractor not initialized")
	}

	return &Manager{
		userProfileService: userProfileService,
		tokenExtractor:     tokenExtractor,
		middleware:         Middleware(userProfileService, tokenExtractor),
	}, nil
}

// Middleware returns the auth middleware function.
// This middleware extracts and injects authentication context into requests.
//
// Usage:
//
//	handler := authManager.Middleware()(mux)
//
// The middleware:
// 1. Extracts Authorization header
// 2. Parses token into user or client principal
// 3. For user principals, creates user record if it's their first login
// 4. Injects context into request
func (m *Manager) Middleware() func(http.Handler) http.Handler { return m.middleware }

// RequireAuthMiddleware returns a middleware that requires authentication.
// If no auth context is found, returns 401 Unauthorized.
// Use this for protected endpoints.
//
// Usage:
//
//	mux.Handle("POST /api/protected",
//	    authManager.RequireAuthMiddleware()(handler),
//	)
func (m *Manager) RequireAuthMiddleware() func(http.Handler) http.Handler {
	return RequireAuth(m.userProfileService, m.tokenExtractor)
}

// OptionalAuthMiddleware returns a middleware for endpoints that work with or without auth.
// This is the same as the basic Middleware() - included for semantic clarity.
//
// Usage:
//
//	mux.Handle("GET /api/products",
//	    authManager.OptionalAuthMiddleware()(handler),
//	)
//
// The handler can check if auth context is available and personalize response.
func (m *Manager) OptionalAuthMiddleware() func(http.Handler) http.Handler { return m.middleware }

// Health checks if the auth system is functioning properly.
// Since the UserProfileService is optional, this only verifies that the auth
// system components are initialized correctly.
//
// Usage in server startup:
//
//	authManager := auth.NewManager(userProfileService, cfg.Auth)
//	if err := authManager.Health(); err != nil {
//	    log.Fatalf("auth system health check failed: %v", err)
//	}
//
// Returns an error if auth system components are misconfigured.
func (m *Manager) Health() error {
	if m.tokenExtractor == nil {
		return fmt.Errorf("token extractor not initialized")
	}
	slog.Info("auth system health check passed", "user_profile_service_enabled", m.userProfileService != nil)
	return nil
}

// Close performs any cleanup needed for the auth manager.
// Currently a no-op, but provided for future extensibility
// (e.g., closing JWT validators, flushing caches, logging stats).
//
// Usage:
//
//	authManager := auth.NewManager(userService, cfg.Auth)
//	defer authManager.Close()
func (m *Manager) Close() error {
	slog.Debug("auth manager closing")
	return nil
}
