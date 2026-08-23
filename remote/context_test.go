// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// headerEchoServer records the headers of the last request it received.
func headerEchoServer(t *testing.T) (*httptest.Server, *http.Header) {
	t.Helper()

	var seen http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	return server, &seen
}

// A header a caller resolves per call reaches the service even though the
// request it travels with was built by someone who knows nothing about it.
func TestContextWithHeaders_Applied(t *testing.T) {
	server, seen := headerEchoServer(t)

	client := NewClient(server.URL)
	ctx := ContextWithHeaders(context.Background(), map[string]string{"X-Filed-For": "org-7"})

	require.NoError(t, client.Request(ctx, Request{Method: "GET", Path: "/ping"}, nil))
	assert.Equal(t, "org-7", seen.Get("X-Filed-For"))
}

// It reaches every transport, not just the JSON one, because they all go through
// the same request builder.
func TestContextWithHeaders_AppliedToEveryTransport(t *testing.T) {
	server, seen := headerEchoServer(t)
	client := NewClient(server.URL)
	ctx := ContextWithHeaders(context.Background(), map[string]string{"X-Filed-For": "org-7"})

	t.Run("raw", func(t *testing.T) {
		_, err := client.RawRequest(ctx, Request{Method: "POST", Path: "/ping", Body: RawBody{Data: []byte("<x/>"), ContentType: "application/xml"}})
		require.NoError(t, err)
		assert.Equal(t, "org-7", seen.Get("X-Filed-For"))
	})

	t.Run("multipart", func(t *testing.T) {
		err := client.Request(ctx, Request{
			Method: "POST", Path: "/ping",
			Body: MultipartBody{Parts: []Part{{Name: "f", FileName: "d.txt", Content: []byte("x")}}},
		}, nil)
		require.NoError(t, err)
		assert.Equal(t, "org-7", seen.Get("X-Filed-For"))
	})
}

// Precedence, weakest first: context, request, authenticator. A request can
// override what the context asked for, and neither can displace authentication.
func TestContextWithHeaders_Precedence(t *testing.T) {
	server, seen := headerEchoServer(t)

	body := fmt.Sprintf(`{"version":"1.0","services":[{"id":"svc","url":%q,
		"auth":{"type":"bearer","options":{"token":"tkn"}}}]}`, server.URL)
	path := filepath.Join(t.TempDir(), "services.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	manager := NewManager()
	require.NoError(t, manager.LoadServices(path))

	ctx := ContextWithHeaders(context.Background(), map[string]string{
		"X-Layer": "context",
		// A context value cannot shadow authentication: the authenticator is
		// applied last, whatever anyone else asked for.
		"Authorization": "Bearer forged",
	})

	t.Run("a context header reaches the service", func(t *testing.T) {
		require.NoError(t, manager.Call(ctx, "svc", Request{Method: "GET", Path: "/ping"}, nil))
		assert.Equal(t, "context", seen.Get("X-Layer"))
		assert.Equal(t, "Bearer tkn", seen.Get("Authorization"))
	})

	t.Run("the request beats the context", func(t *testing.T) {
		err := manager.Call(ctx, "svc", Request{
			Method:  "GET",
			Path:    "/ping",
			Headers: map[string]string{"X-Layer": "request"},
		}, nil)
		require.NoError(t, err)
		assert.Equal(t, "request", seen.Get("X-Layer"))
		assert.Equal(t, "Bearer tkn", seen.Get("Authorization"))
	})
}

func TestContextWithHeaders_MergesAndCopies(t *testing.T) {
	t.Run("a nested caller adds rather than replaces", func(t *testing.T) {
		ctx := ContextWithHeaders(context.Background(), map[string]string{"A": "1", "B": "1"})
		ctx = ContextWithHeaders(ctx, map[string]string{"B": "2", "C": "2"})

		assert.Equal(t, map[string]string{"A": "1", "B": "2", "C": "2"}, headersFromContext(ctx))
	})

	t.Run("empty is a no-op", func(t *testing.T) {
		base := context.Background()
		assert.Equal(t, base, ContextWithHeaders(base, nil))
		assert.Equal(t, base, ContextWithHeaders(base, map[string]string{}))
	})

	// Header names are case-insensitive, so a nested caller overrides an outer
	// one whatever spelling either used. Before names were canonicalized these
	// were two map entries and http.Header.Set applied them in map iteration
	// order, so the inner value won only sometimes.
	t.Run("a nested caller overrides whatever the spelling", func(t *testing.T) {
		ctx := ContextWithHeaders(context.Background(), map[string]string{"X-Tenant": "outer"})
		ctx = ContextWithHeaders(ctx, map[string]string{"x-tenant": "inner"})

		carried := headersFromContext(ctx)
		assert.Len(t, carried, 1, "one header, not one per spelling")
		assert.Equal(t, "inner", carried["X-Tenant"])
	})

	t.Run("the caller's map is copied", func(t *testing.T) {
		headers := map[string]string{"A": "1"}
		ctx := ContextWithHeaders(context.Background(), headers)
		headers["A"] = "mutated"

		assert.Equal(t, "1", headersFromContext(ctx)["A"])
	})

	t.Run("a context carrying none reads as none", func(t *testing.T) {
		assert.Nil(t, headersFromContext(context.Background()))
	})
}

// A client used without the helper behaves exactly as before.
func TestContextWithHeaders_AbsentChangesNothing(t *testing.T) {
	server, seen := headerEchoServer(t)
	client := NewClient(server.URL)

	require.NoError(t, client.Request(context.Background(), Request{
		Method: "GET", Path: "/ping", Headers: map[string]string{"X-Own": "1"},
	}, nil))

	assert.Equal(t, "1", seen.Get("X-Own"))
	assert.Equal(t, http.Header{}.Get("X-Filed-For"), seen.Get("X-Filed-For"))
}

// ContextWithHeaderValues is the seam for a caller whose values arrive untyped —
// decoded from JSON, or assembled by an engine from a template.
// The symptom the canonicalizing merge prevents, at the wire.
func TestContextWithHeaders_NestedOverrideReachesTheService(t *testing.T) {
	server, seen := headerEchoServer(t)
	client := NewClient(server.URL)

	ctx := ContextWithHeaders(context.Background(), map[string]string{"X-Tenant": "outer"})
	ctx = ContextWithHeaders(ctx, map[string]string{"x-tenant": "inner"})

	require.NoError(t, client.Request(ctx, Request{Method: "GET", Path: "/ping"}, nil))
	assert.Equal(t, "inner", seen.Get("X-Tenant"))
	assert.Len(t, seen.Values("X-Tenant"), 1)
}

func TestContextWithHeaderValues(t *testing.T) {
	t.Run("string values are carried", func(t *testing.T) {
		ctx, err := ContextWithHeaderValues(context.Background(), map[string]any{
			"X-Filed-For": "org-7",
		})
		require.NoError(t, err)
		assert.Equal(t, "org-7", headersFromContext(ctx)["X-Filed-For"])
	})

	t.Run("nothing to carry leaves the context alone", func(t *testing.T) {
		base := context.Background()

		for name, values := range map[string]map[string]any{
			"nil":   nil,
			"empty": {},
		} {
			t.Run(name, func(t *testing.T) {
				ctx, err := ContextWithHeaderValues(base, values)
				require.NoError(t, err)
				assert.Equal(t, base, ctx)
			})
		}
	})

	// A header this package cannot send is a mistake in whatever built the map.
	t.Run("a value that is not a string is an error", func(t *testing.T) {
		_, err := ContextWithHeaderValues(context.Background(), map[string]any{"X-Count": 7})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `header "X-Count" must be a string, got int`)
	})

	t.Run("one header named twice under different spellings is an error", func(t *testing.T) {
		_, err := ContextWithHeaderValues(context.Background(), map[string]any{
			"X-Tenant": "a",
			"x-tenant": "b",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `header "X-Tenant" is named more than once`)
	})

	t.Run("a mixed-case name is carried canonically", func(t *testing.T) {
		ctx, err := ContextWithHeaderValues(context.Background(), map[string]any{"x-tenant": "v"})
		require.NoError(t, err)
		assert.Equal(t, "v", headersFromContext(ctx)["X-Tenant"])
	})

	t.Run("an empty header name is an error", func(t *testing.T) {
		_, err := ContextWithHeaderValues(context.Background(), map[string]any{"": "v"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "a header name is empty")
	})

	// The one thing that is skipped rather than refused: an optional source that
	// resolved to blank. The call still goes out, and the service answers for
	// itself.
	t.Run("an empty value is dropped, not refused", func(t *testing.T) {
		ctx, err := ContextWithHeaderValues(context.Background(), map[string]any{
			"X-Optional": "",
			"X-Present":  "v",
		})
		require.NoError(t, err)

		carried := headersFromContext(ctx)
		assert.NotContains(t, carried, "X-Optional")
		assert.Equal(t, "v", carried["X-Present"])
	})

	// Errors leave nothing behind: a refused map must not half-apply.
	t.Run("an error carries no headers", func(t *testing.T) {
		base := ContextWithHeaders(context.Background(), map[string]string{"X-Existing": "1"})
		ctx, err := ContextWithHeaderValues(base, map[string]any{"X-Bad": 7})
		require.Error(t, err)
		assert.Equal(t, base, ctx)
		assert.Equal(t, map[string]string{"X-Existing": "1"}, headersFromContext(ctx))
	})
}
