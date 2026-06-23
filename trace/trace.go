// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type contextKey string

// traceIDKey is the context key used to propagate the Trace ID.
const traceIDKey contextKey = "trace_id"

// ContextWithTraceID returns a new context with the given trace ID injected.
func ContextWithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// GetTraceID extracts the trace ID from the context.
func GetTraceID(ctx context.Context) string {
	if v := ctx.Value(traceIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// TraceMiddleware extracts a trace ID from incoming headers and injects it into the request context.
// If no trace ID is found, it generates a fallback trace ID to ensure request correlation.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reuse trace/correlation IDs from incoming request headers to preserve
		// end-to-end trace propagation across service boundaries (e.g., API Gateway).
		var traceID string
		if tid := r.Header.Get("X-Trace-ID"); tid != "" {
			traceID = tid
		} else if cid := r.Header.Get("X-Correlation-ID"); cid != "" {
			traceID = cid
		} else if rid := r.Header.Get("X-Request-ID"); rid != "" {
			traceID = rid
		}

		if traceID != "" && !isValidTraceID(traceID) {
			traceID = ""
		}

		if traceID == "" {
			traceID = generateTraceID()
		}

		w.Header().Set("X-Trace-ID", traceID)
		ctx := ContextWithTraceID(r.Context(), traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isValidTraceID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		isSpecial := c == '-' || c == '_' || c == ':' || c == '.' || c == '/' || c == '='
		if !isAlphaNum && !isSpecial {
			return false
		}
	}
	return true
}

func generateTraceID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
