// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	oteltrace "go.opentelemetry.io/otel/trace"
)

type contextKey string

const (
	traceIDKey         contextKey = "trace_id"
	clientRequestIDKey contextKey = "client_request_id"
	slotKey            contextKey = "trace_slot"
)

// slot is a mutable correlation holder shared between TraceSlotMiddleware
// (outside logging) and TraceMiddleware (often inside otelhttp). Outer log
// middleware that keeps the pre-handler request can still read GetTraceID.
type slot struct {
	traceID         string
	clientRequestID string
}

// TraceSlotMiddleware allocates a mutable correlation slot on the request
// context. Place it outside any middleware that logs after the handler using
// the outer request context (for example systemlog), and place TraceMiddleware
// inside the OTel HTTP handler so it can take the active span's TraceID.
// TraceMiddleware writes the server correlation id into the slot so those
// outer log lines still get traceId via GetTraceID.
func TraceSlotMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), slotKey, &slot{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ContextWithTraceID returns a new context with the given trace ID injected.
func ContextWithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// GetTraceID extracts the server correlation / trace ID from the context.
// It prefers the value set on the context, then any TraceSlotMiddleware slot.
func GetTraceID(ctx context.Context) string {
	if v := ctx.Value(traceIDKey); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	if s, ok := ctx.Value(slotKey).(*slot); ok && s != nil && s.traceID != "" {
		return s.traceID
	}
	return ""
}

// ContextWithClientRequestID returns a new context with the caller's request
// id (from X-Trace-ID / X-Correlation-ID / X-Request-ID) for matching only.
func ContextWithClientRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, clientRequestIDKey, id)
}

// GetClientRequestID returns the caller's request id when one was supplied
// and validated. It is never used as the server correlation id.
func GetClientRequestID(ctx context.Context) string {
	if v := ctx.Value(clientRequestIDKey); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	if s, ok := ctx.Value(slotKey).(*slot); ok && s != nil && s.clientRequestID != "" {
		return s.clientRequestID
	}
	return ""
}

// TraceMiddleware assigns a server-generated correlation id and injects it
// into the request context and the X-Trace-ID response header.
//
// When the request context already has a valid OpenTelemetry span (for
// example after otelhttp), that span's TraceID is used so logs and spans
// match. Otherwise a random 32-character hex id is generated.
//
// Caller-supplied X-Trace-ID, X-Correlation-ID, and X-Request-ID values are
// kept only as GetClientRequestID / clientRequestId in logs. They are never
// used as the correlation id.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID := clientRequestIDFromHeaders(r)
		traceID := serverTraceID(r.Context())

		w.Header().Set("X-Trace-ID", traceID)
		ctx := ContextWithTraceID(r.Context(), traceID)
		if clientID != "" {
			ctx = ContextWithClientRequestID(ctx, clientID)
		}
		if s, ok := ctx.Value(slotKey).(*slot); ok && s != nil {
			s.traceID = traceID
			s.clientRequestID = clientID
		}
		// WithContext returns a shallow copy; ServeMux sets Pattern on that
		// copy. Copy it back so outer otelhttp can still label http.route.
		child := r.WithContext(ctx)
		next.ServeHTTP(w, child)
		r.Pattern = child.Pattern
	})
}

func serverTraceID(ctx context.Context) string {
	if sc := oteltrace.SpanContextFromContext(ctx); sc.IsValid() {
		return sc.TraceID().String()
	}
	return generateTraceID()
}

func clientRequestIDFromHeaders(r *http.Request) string {
	for _, name := range []string{"X-Trace-ID", "X-Correlation-ID", "X-Request-ID"} {
		if v := r.Header.Get(name); v != "" && isValidClientRequestID(v) {
			return v
		}
	}
	return ""
}

func isValidClientRequestID(id string) bool {
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
