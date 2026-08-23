// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"context"
	"fmt"
	"log/slog"
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
	merged := make(map[string]string, len(headers))
	for k, v := range headersFromContext(ctx) {
		merged[k] = v
	}
	for k, v := range headers {
		merged[k] = v
	}
	return context.WithValue(ctx, headersContextKey{}, merged)
}

// ContextWithHeaderValues is ContextWithHeaders for a caller holding values it
// has not typed — a map decoded from JSON, or one an engine assembled from a
// template — rather than strings it already checked.
//
// A value that is not a string, or an empty header name, is an error rather than
// a skipped header: whatever produced the map named a header this package cannot
// send, and learning that from the service's rejection is worse than failing
// here. A value that is present but empty is the exception, dropped with a
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

		str, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("header %q must be a string, got %T", name, value)
		}
		if str == "" {
			slog.WarnContext(ctx, "remote: dropping a header whose value is empty", "header", name)
			continue
		}
		headers[name] = str
	}
	return headers, nil
}

// headersFromContext returns the headers carried by ctx, or nil.
func headersFromContext(ctx context.Context) map[string]string {
	headers, _ := ctx.Value(headersContextKey{}).(map[string]string)
	return headers
}
