// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_JSONRequest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/test-path", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "custom-value", r.Header.Get("X-Custom-Header"))
		assert.Equal(t, "val1", r.URL.Query().Get("param1"))

		var body map[string]string
		err := json.NewDecoder(r.Body).Decode(&body)
		require.NoError(t, err)
		assert.Equal(t, "world", body["hello"])

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)

	query := url.Values{}
	query.Set("param1", "val1")

	req := Request{
		Method:  http.MethodPost,
		Path:    "/test-path",
		Query:   query,
		Body:    map[string]string{"hello": "world"},
		Headers: map[string]string{"X-Custom-Header": "custom-value"},
	}

	var resp map[string]string
	err := client.JSONRequest(context.Background(), req, &resp)

	assert.NoError(t, err)
	assert.Equal(t, "ok", resp["status"])
}

func TestClient_RetryLogic(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		curr := atomic.LoadInt32(&attempts)
		if curr < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"recovered"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)

	retryCfg := RetryConfig{
		MaxRetries:      3,
		InitialBackoff:  10 * time.Millisecond,
		MaxBackoff:      50 * time.Millisecond,
		RetryableStatus: []int{http.StatusServiceUnavailable},
	}

	req := Request{
		Method: http.MethodGet,
		Path:   "/retry",
		Retry:  &retryCfg,
	}

	var resp map[string]string
	err := client.JSONRequest(context.Background(), req, &resp)

	assert.NoError(t, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
	assert.Equal(t, "recovered", resp["status"])
}

func TestClient_RetryExhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := NewClient(server.URL)

	retryCfg := RetryConfig{
		MaxRetries:      2,
		InitialBackoff:  1 * time.Millisecond,
		MaxBackoff:      5 * time.Millisecond,
		RetryableStatus: []int{http.StatusTooManyRequests},
	}

	req := Request{
		Method: http.MethodGet,
		Path:   "/retry-limit",
		Retry:  &retryCfg,
	}

	err := client.JSONRequest(context.Background(), req, nil)
	assert.Error(t, err)
}

func TestClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	req := Request{
		Method: http.MethodGet,
		Path:   "/timeout",
	}

	err := client.JSONRequest(ctx, req, nil)
	assert.ErrorIs(t, err, ErrTimeout)
}

func TestClient_BaseURL_Logic(t *testing.T) {
	t.Run("panics with empty baseURL", func(t *testing.T) {
		assert.Panics(t, func() {
			NewClient("")
		})
	})

	t.Run("absolute path verified against baseURL", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		// Should PASS if it matches
		client := NewClient(server.URL)
		err := client.JSONRequest(context.Background(), Request{Method: "GET", Path: server.URL + "/foo"}, nil)
		assert.NoError(t, err)

		// Should FAIL if it doesn't match
		client2 := NewClient("http://wrong-base.local")
		err2 := client2.JSONRequest(context.Background(), Request{Method: "GET", Path: server.URL}, nil)
		assert.Error(t, err2)
		assert.Contains(t, err2.Error(), "does not match configured service host")
	})

	// An empty path addresses the service URL itself. Appending a separator
	// would request a different resource, and some servers answer the trailing
	// form with 405.
	t.Run("relative path joins without a trailing separator", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			basePath  string
			path      string
			query     url.Values
			wantPath  string
			wantQuery string
		}{
			{name: "empty path posts to the service URL itself", basePath: "/api/reports", path: "", wantPath: "/api/reports"},
			{name: "root path is treated as empty", basePath: "/api/reports", path: "/", wantPath: "/api/reports"},
			{name: "non-empty path is appended", basePath: "/api", path: "reports", wantPath: "/api/reports"},
			{name: "leading separator is not doubled", basePath: "/api", path: "/reports", wantPath: "/api/reports"},

			// Query parameters are appended to the path before it reaches the
			// join, so the empty-path rule has to look past them: a path of
			// "?a=1" still addresses the service URL itself.
			{
				name:      "empty path with query keeps the service URL itself",
				basePath:  "/api/reports",
				path:      "",
				query:     url.Values{"id": {"42"}},
				wantPath:  "/api/reports",
				wantQuery: "id=42",
			},
			{
				name:      "root path with query is treated as empty",
				basePath:  "/api/reports",
				path:      "/",
				query:     url.Values{"id": {"42"}},
				wantPath:  "/api/reports",
				wantQuery: "id=42",
			},
			{
				name:      "non-empty path with query is unaffected",
				basePath:  "/api",
				path:      "/reports",
				query:     url.Values{"id": {"42"}},
				wantPath:  "/api/reports",
				wantQuery: "id=42",
			},
			{
				name:      "query already in the path is preserved",
				basePath:  "/api/reports",
				path:      "?first=1",
				query:     url.Values{"second": {"2"}},
				wantPath:  "/api/reports",
				wantQuery: "first=1&second=2",
			},

			// Resolution goes through net/url, so escaping and dot-segments
			// follow the usual rules rather than whatever concatenation left.
			{
				name:     "an escaped separator is not turned into a real one",
				basePath: "/api",
				path:     "x%2Fy",
				wantPath: "/api/x%2Fy",
			},
			{
				name:     "a space in a segment is escaped",
				basePath: "/api",
				path:     "sp ace",
				wantPath: "/api/sp%20ace",
			},
			{
				name:     "dot segments are resolved before sending",
				basePath: "/api/reports",
				path:     "../other",
				wantPath: "/api/other",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var gotPath, gotQuery string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// EscapedPath, not Path: the difference between a literal
					// separator and an escaped one is the point of two cases here.
					gotPath = r.URL.EscapedPath()
					gotQuery = r.URL.RawQuery
					w.WriteHeader(http.StatusOK)
				}))
				defer server.Close()

				client := NewClient(server.URL + tc.basePath)
				err := client.JSONRequest(context.Background(), Request{Method: "GET", Path: tc.path, Query: tc.query}, nil)
				assert.NoError(t, err)
				assert.Equal(t, tc.wantPath, gotPath)
				assert.Equal(t, tc.wantQuery, gotQuery)
			})
		}
	})
}

func TestClient_NoContentResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	var resp map[string]any
	err := client.JSONRequest(context.Background(), Request{Method: "GET", Path: "/"}, &resp)
	assert.NoError(t, err)
	assert.Nil(t, resp)
}

func TestClient_MarshalError(t *testing.T) {
	client := NewClient("http://local")
	// Channels cannot be marshaled to JSON
	req := Request{
		Method: "POST",
		Path:   "/",
		Body:   make(chan int),
	}
	err := client.JSONRequest(context.Background(), req, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marshal payload")
}

func TestClient_HttpErrors(t *testing.T) {
	tests := []struct {
		code int
		err  error
	}{
		{http.StatusNotFound, ErrNotFound},
		{http.StatusBadRequest, ErrBadRequest},
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, ErrUnauthorized},
		{http.StatusServiceUnavailable, ErrServiceUnavailable},
		{http.StatusTeapot, ErrRequestFailed},
	}

	for _, tt := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.code)
		}))

		client := NewClient(server.URL)
		err := client.JSONRequest(context.Background(), Request{Method: "GET", Path: "/"}, nil)
		assert.ErrorIs(t, err, tt.err)
		server.Close()
	}
}

func TestClient_DecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{ "bad": "json"`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	var resp map[string]any
	err := client.JSONRequest(context.Background(), Request{Method: "GET", Path: "/"}, &resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode response")
}
