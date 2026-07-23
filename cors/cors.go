// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package cors

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// CORS creates a middleware that handles CORS (Cross-Origin Resource Sharing) requests
func CORS(cfg *Config) func(http.Handler) http.Handler {
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
			match := matchOrigin(origin, cfg.AllowedOrigins)
			if match == matchNone {
				// Origin is present but not allowed
				slog.DebugContext(r.Context(), "CORS request from disallowed origin blocked",
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
			if match == matchExplicit {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			} else {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}
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
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// For actual requests, log and continue to the next handler.
			slog.DebugContext(r.Context(), "CORS headers set for allowed origin",
				"origin", origin,
				"method", r.Method,
				"path", r.URL.Path,
			)
			next.ServeHTTP(w, r)
		})
	}
}

type originMatch int

const (
	matchNone originMatch = iota
	matchExplicit
	matchWildcard
)

// matchOrigin inspects allowedOrigins in a single pass to determine if origin matches
// explicitly, via wildcard (*), or not at all.
func matchOrigin(origin string, allowedOrigins []string) originMatch {
	hasWildcard := false
	for _, allowed := range allowedOrigins {
		if allowed == origin {
			return matchExplicit
		}
		if allowed == "*" {
			hasWildcard = true
		}
	}
	if hasWildcard {
		return matchWildcard
	}
	return matchNone
}
