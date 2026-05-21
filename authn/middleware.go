package auth

import (
	"context"
	"log/slog"
	"net/http"
)

// Middleware creates an HTTP middleware that extracts and injects authentication context.
// This middleware:
// 1. Extracts the Authorization header
// 2. Parses the token into a user principal or client principal
// 3. For user principals, resolves user profile idempotently if UserProfileService is provided
// 4. Injects the auth context into the request
//
// Behavior summary:
// - Missing Authorization header: request proceeds without auth context.
// - Invalid token: request is rejected with 401.
// - Auth dependencies unavailable: request is rejected with 500.
// - User principal on first login: resolves (get-or-create) user profile if service is provided.
//
// This design allows:
// - Public endpoints (no auth required)
// - Protected endpoints (check for context)
// - Optional auth endpoints (use context if available)
// - Generic auth that works with or without a user profile service
func Middleware(userProfileService UserProfileService, tokenExtractor *TokenExtractor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				slog.Debug("no authorization header provided")
				next.ServeHTTP(w, r)
				return
			}

			if tokenExtractor == nil {
				slog.Error("auth middleware: token extractor not initialized")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal_server_error","message":"authentication subsystem not initialized"}`))
				return
			}

			principal, err := tokenExtractor.ExtractPrincipalFromHeader(authHeader)
			if err != nil {
				slog.Warn("failed to extract principal from token", "error", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized","message":"invalid authentication token"}`))
				return
			}

			if principal == nil {
				slog.Warn("token extractor returned nil principal")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized","message":"invalid authentication token"}`))
				return
			}

			if principal.UserPrincipal == nil && principal.ClientPrincipal == nil {
				slog.Warn("token missing both userPrincipal and clientPrincipal")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized","message":"invalid authentication token"}`))
				return
			}

			authCtx := buildAuthContext(principal)
			if principal.UserPrincipal != nil && userProfileService != nil {
				user := principal.UserPrincipal
				userID, err := userProfileService.GetOrCreateUser(
					user.UserID,
					user.Email,
					derefString(user.PhoneNumber),
					user.OUID,
					user.OUHandle,
				)
				if err != nil {
					slog.Error("failed to get or create user profile", "idp_user_id", user.UserID, "error", err)
				} else if userID != nil {
					authCtx.User.ID = *userID
					slog.Debug("resolved user profile", "idp_user_id", user.UserID, "user_id", *userID)
				}
			}

			ctx := context.WithValue(r.Context(), AuthContextKey, authCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth returns a middleware that requires authentication.
// If no auth context is found, returns 401 Unauthorized.
// This middleware should be applied to protected endpoints.
//
// Usage:
//
//	mux.Handle("POST /api/protected", auth.RequireAuth(userProfileService, tokenExtractor)(handler))
//
// TODO_JWT_FUTURE: Consider adding:
// - Different auth levels (basic, standard, admin)
// - Claim validation beyond token signature
// - Rate limiting per user
func RequireAuth(userProfileService UserProfileService, tokenExtractor *TokenExtractor) func(http.Handler) http.Handler {
	authMiddleware := Middleware(userProfileService, tokenExtractor)
	return func(next http.Handler) http.Handler {
		return authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if GetAuthContext(r.Context()) == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized","message":"authentication required"}`))
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}
