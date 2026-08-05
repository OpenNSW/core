// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"context"
	"net/http"
)

// contextKey is a private type for context keys defined by this package,
// avoiding collisions with keys defined elsewhere.
type contextKey string

// requestContextKey is the context key HTTPHandler uses to make the inbound
// *http.Request reachable from a gateway's VerifyWebhook.
const requestContextKey contextKey = "payment_inbound_request"

// ContextWithRequest returns a new context with the given inbound HTTP
// request attached. HTTPHandler calls this — for every request, on both
// HandleValidateReference and HandleWebhook — before invoking PaymentService,
// so that by the time a gateway's VerifyWebhook runs, anything about the
// request beyond the explicit body and headers parameters (method, URL,
// query parameters, TLS connection state, remote address, cookies, trailers,
// or any other field of *http.Request) is reachable via RequestFromContext.
// core/payment never has to anticipate which specific dimension a given
// verification scheme needs; a gateway pulls out whatever it requires.
//
// By the time this is called, HTTPHandler has already drained r.Body via
// io.ReadAll to produce the explicit body []byte parameter passed to
// VerifyWebhook/ParseWebhook/etc. — gateways must use that parameter for the
// payload; r.Body here will read as empty, not the original payload. This
// mechanism is for everything else about the request, not for re-reading the
// body.
//
// r itself is stored as given — its own r.Context() at this point is
// whatever it was before this call (e.g. net/http's server-assigned
// context), not the context this function returns. RequestFromContext
// reconciles that on retrieval: see its doc comment.
func ContextWithRequest(ctx context.Context, r *http.Request) context.Context {
	return context.WithValue(ctx, requestContextKey, r)
}

// RequestFromContext extracts the inbound *http.Request previously attached
// via ContextWithRequest, or nil if none was ever attached — e.g. when
// PaymentService is invoked directly, bypassing HTTPHandler (as most unit
// tests do). Callers (typically a gateway's VerifyWebhook) must treat a nil
// return as "not available" and fall back to whatever the explicit
// body/headers parameters allow, rather than treating nil as a zero-value
// request. The returned *http.Request must not be mutated: HTTPHandler is
// still using the underlying request to serve the response after
// PaymentService returns, and other callers may hold their own copy.
//
// The returned request is rewrapped via r.WithContext(ctx) using the exact
// ctx this function is called with, so its own .Context() is that same ctx
// — not just whatever context ContextWithRequest happened to return earlier.
// This stays consistent even if ctx is wrapped further after
// ContextWithRequest ran, since context.Value lookups delegate to parent
// contexts regardless of how many layers were added afterward. One
// consequence: repeated calls to RequestFromContext, even with the same ctx,
// return distinct *http.Request instances (shallow copies sharing the same
// underlying Header/URL/Body/etc.), not the exact same pointer every time.
func RequestFromContext(ctx context.Context) *http.Request {
	if v := ctx.Value(requestContextKey); v != nil {
		if r, ok := v.(*http.Request); ok && r != nil {
			return r.WithContext(ctx)
		}
	}
	return nil
}
