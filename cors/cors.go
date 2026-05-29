package middleware

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/OpenNSW/nsw/backend/internal/config"
)

// CORS creates a middleware that handles CORS (Cross-Origin Resource Sharing) requests
func CORS(cfg *config.CORSConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Not a CORS request, pass through.
				next.ServeHTTP(w, r)
				return
			}

			// For any request with an Origin header, we should indicate that the response may vary.
			w.Header().Add("Vary", "Origin")

			// Check if the origin is allowed
			if !isOriginAllowed(origin, cfg.AllowedOrigins) {
				// Origin is present but not allowed
				slog.Warn("CORS request from disallowed origin blocked",
					"origin", origin,
					"method", r.Method,
					"path", r.URL.Path,
					"allowed_origins", cfg.AllowedOrigins,
				)
				// For preflight requests from disallowed origins, we must still respond to the OPTIONS method.
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				// For actual requests, we pass through. The browser will block the response.
				next.ServeHTTP(w, r)
				return
			}

			// Origin is allowed. Set common headers.
			w.Header().Set("Access-Control-Allow-Origin", origin)
			if cfg.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			// Handle preflight OPTIONS request
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(cfg.AllowedMethods, ", "))
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowedHeaders, ", "))
				if cfg.MaxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))
				}
				slog.Info("CORS preflight request handled",
					"origin", origin,
					"path", r.URL.Path,
				)
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// For actual requests, log and continue to the next handler.
			slog.Debug("CORS headers set for allowed origin",
				"origin", origin,
				"method", r.Method,
				"path", r.URL.Path,
			)
			next.ServeHTTP(w, r)
		})
	}
}

// isOriginAllowed checks if the given origin is in the list of allowed origins
func isOriginAllowed(origin string, allowedOrigins []string) bool {
	for _, allowed := range allowedOrigins {
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	return false
}
