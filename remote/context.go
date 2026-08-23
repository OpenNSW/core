// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// headersContextKey is the private key the request headers travel under.
type headersContextKey struct{}

// ContextWithHeaders returns a context carrying headers to add to every request
// made with it.
//
// It exists for the caller that resolves a header but does not build the
// request. A generic plugin assembles Request from a template and an
// interpreter, so anything it does not model — a header whose value belongs to
// the case being processed rather than to the service — has nowhere to go. Its
// caller has the value and the context, and this is the seam between them.
//
// Precedence, weakest first: these, the per-request Request.Headers, and last the
// authenticator. So a call that does model its headers still wins, and no context
// value can displace authentication.
//
// A header the request builder can set itself belongs on Request instead.
func ContextWithHeaders(ctx context.Context, headers map[string]string) context.Context {
	if len(headers) == 0 {
		return ctx
	}

	// Merge with anything already carried, so a nested caller adds to the set
	// rather than replacing what an outer one put there. The map is copied so
	// that a later mutation by the caller cannot change what a request already
	// in flight sends.
	//
	// Names are canonicalized on the way in, because header names are
	// case-insensitive but map keys are not: without this an inner "x-tenant"
	// and an outer "X-Tenant" are two entries, and which one http.Header.Set
	// applies last is map iteration order. What is already on the context was
	// canonicalized when it was stored.
	merged := make(map[string]string, len(headers))
	for k, v := range headersFromContext(ctx) {
		merged[k] = v
	}
	for k, v := range headers {
		merged[http.CanonicalHeaderKey(k)] = v
	}
	return context.WithValue(ctx, headersContextKey{}, merged)
}

// ContextWithHeaderValues is ContextWithHeaders for a caller holding values it
// has not typed — a map decoded from JSON, or one an engine assembled from a
// template — rather than strings it already checked.
//
// A value that is not a string, or a name net/http could not send, is an error
// rather than a skipped header: whatever produced the map named a header this
// package cannot send, and an invalid name fails the whole request from inside
// the transport, quoting nothing the caller would recognise. A value that is present but empty is the exception, dropped with a
// warning, so a caller whose source was optional and resolved to blank still
// makes its call.
func ContextWithHeaderValues(ctx context.Context, values map[string]any) (context.Context, error) {
	headers, err := headerValues(ctx, values)
	if err != nil {
		return ctx, err
	}
	return ContextWithHeaders(ctx, headers), nil
}

// headerValues checks an untyped map and returns the headers worth sending.
func headerValues(ctx context.Context, values map[string]any) (map[string]string, error) {
	headers := make(map[string]string, len(values))
	for name, value := range values {
		if name == "" {
			return nil, fmt.Errorf("a header name is empty")
		}
		if !validHeaderName(name) {
			return nil, fmt.Errorf("header name %q is not a valid HTTP header name", name)
		}

		str, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("header %q must be a string, got %T", name, value)
		}
		if str == "" {
			slog.WarnContext(ctx, "remote: dropping a header whose value is empty", "header", name)
			continue
		}

		// Two spellings of one header in the same map is the same ambiguity the
		// merge avoids, except here it cannot be resolved by precedence: nothing
		// says which the caller meant. Map keys are unique, so a collision after
		// canonicalizing always means the name was written twice.
		canonical := http.CanonicalHeaderKey(name)
		if _, duplicated := headers[canonical]; duplicated {
			return nil, fmt.Errorf("header %q is named more than once, under different spellings", canonical)
		}
		headers[canonical] = str
	}
	return headers, nil
}

// validHeaderName reports whether name is a token, the rule net/http enforces
// when it writes the request.
//
// Checking here rather than letting the request fail turns "invalid header field
// name" — raised deep in the transport, naming nothing the caller wrote — into an
// error that quotes the offending name. It catches a name that is only
// whitespace, and equally one with a space or a colon inside it.
func validHeaderName(name string) bool {
	for i := 0; i < len(name); i++ {
		if !isTokenChar(name[i]) {
			return false
		}
	}
	return name != ""
}

// isTokenChar reports whether c may appear in a header field name (RFC 9110).
func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
	}
}

// headersFromContext returns the headers carried by ctx, or nil.
func headersFromContext(ctx context.Context) map[string]string {
	headers, _ := ctx.Value(headersContextKey{}).(map[string]string)
	return headers
}
