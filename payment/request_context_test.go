// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextWithRequest_RoundTrip(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	ctx := ContextWithRequest(context.Background(), req)

	got := RequestFromContext(ctx)
	require.NotNil(t, got)
	assert.Equal(t, req.Method, got.Method)
	assert.Same(t, req.URL, got.URL, "RequestFromContext should return a shallow copy sharing the same *url.URL")
}

func TestRequestFromContext_AbsentReturnsNil(t *testing.T) {
	assert.Nil(t, RequestFromContext(context.Background()))
}

func TestContextWithRequest_NilRequestIsSafe(t *testing.T) {
	assert.NotPanics(t, func() {
		ctx := ContextWithRequest(context.Background(), nil)
		assert.Nil(t, RequestFromContext(ctx))
	})
}

// TestRequestFromContext_ContextMatchesCaller guards against the bug where
// ContextWithRequest stored the caller's original *http.Request as-is: that
// request's own r.Context() was whatever it was BEFORE ContextWithRequest
// ran (net/http's server-assigned context), not the enriched ctx
// ContextWithRequest returned — so a gateway doing
// RequestFromContext(ctx).Context() got a poorer, different context than
// the ctx it was directly handed, missing requestContextKey itself and
// anything else already on ctx. RequestFromContext must rebuild the request
// using the exact ctx it's called with, so this stays in sync.
func TestRequestFromContext_ContextMatchesCaller(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	ctx := ContextWithRequest(context.Background(), req)

	got := RequestFromContext(ctx)
	require.NotNil(t, got)
	assert.Same(t, ctx, got.Context(),
		"RequestFromContext(ctx).Context() must be ctx itself, not whatever context req had before ContextWithRequest ran")
}

// TestRequestFromContext_SeesValuesFromOriginalContext demonstrates the
// practical consequence of the bug above directly: a value already present
// on ctx before ContextWithRequest ran must still be visible via the
// returned request's own Context() — under the old code it was not, since
// req.Context() had no relationship to ctx at all.
func TestRequestFromContext_SeesValuesFromOriginalContext(t *testing.T) {
	type traceIDKey struct{}
	parent := context.WithValue(context.Background(), traceIDKey{}, "abc123")

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	ctx := ContextWithRequest(parent, req)

	got := RequestFromContext(ctx)
	require.NotNil(t, got)
	assert.Equal(t, "abc123", got.Context().Value(traceIDKey{}),
		"a value present on ctx before ContextWithRequest ran must still be visible via the returned request's own Context()")
}

// TestRequestFromContext_SeesValuesAddedAfterAttachment proves the stronger
// guarantee: RequestFromContext reconstructs from whatever ctx it's actually
// called with, so a value added to ctx AFTER ContextWithRequest ran (e.g. if
// something between HTTPHandler and the eventual VerifyWebhook call wraps
// ctx further — not the case anywhere in this package today, but worth
// guarding) is still visible, not just values present at attachment time.
func TestRequestFromContext_SeesValuesAddedAfterAttachment(t *testing.T) {
	type laterKey struct{}
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	ctx := ContextWithRequest(context.Background(), req)
	ctx2 := context.WithValue(ctx, laterKey{}, "later-value")

	got := RequestFromContext(ctx2)
	require.NotNil(t, got)
	assert.Equal(t, "later-value", got.Context().Value(laterKey{}),
		"a value added to ctx after ContextWithRequest ran must still be visible via the returned request's own Context()")
}
