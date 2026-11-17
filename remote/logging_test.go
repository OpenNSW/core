// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenNSW/core/remote/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logCapture is a Debug-level JSON logger whose records the test can inspect.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newLogCapture() (*logCapture, *slog.Logger) {
	c := &logCapture{}
	return c, slog.New(slog.NewJSONHandler(c, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *logCapture) raw() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (c *logCapture) records(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(c.raw()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
		out = append(out, rec)
	}
	return out
}

// find returns the records with message msg.
func (c *logCapture) find(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, rec := range c.records(t) {
		if rec["msg"] == msg {
			out = append(out, rec)
		}
	}
	return out
}

func TestClient_WithLogger_LogsRequestWithoutQueryString(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")

	capture, logger := newLogCapture()
	client := NewClient(server.URL, WithLogger(logger))

	err := client.Request(context.Background(), Request{
		Method: http.MethodGet,
		Path:   "/v1/items?token=path-secret",
		Query:  url.Values{"key": {"query-secret"}},
	}, nil)
	require.NoError(t, err)

	started := capture.find(t, "remote: request started")
	require.Len(t, started, 1)
	assert.Equal(t, "DEBUG", started[0]["level"])
	assert.Equal(t, http.MethodGet, started[0][attrMethod])
	assert.Equal(t, host, started[0][attrServerAddress])
	assert.Equal(t, "/v1/items", started[0][attrURLPath])
	assert.NotContains(t, started[0], attrResendCount)

	completed := capture.find(t, "remote: request completed")
	require.Len(t, completed, 1)
	assert.Equal(t, "DEBUG", completed[0]["level"])
	assert.EqualValues(t, http.StatusOK, completed[0][attrStatusCode])

	assert.NotContains(t, capture.raw(), "secret", "the query string must never be logged")
}

func TestClient_FailedRequestIsLoggedAtDebug(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	serverURL := server.URL
	server.Close() // connection refused

	capture, logger := newLogCapture()
	client := NewClient(serverURL, WithLogger(logger))

	err := client.Request(context.Background(), Request{Method: http.MethodGet, Path: "/down"}, nil)
	require.ErrorIs(t, err, ErrRequestFailed)

	failed := capture.find(t, "remote: request failed")
	require.Len(t, failed, 1)
	assert.Equal(t, "DEBUG", failed[0]["level"])
	assert.Equal(t, "_OTHER", failed[0][attrErrorType])
	assert.NotEmpty(t, failed[0]["error"])

	for _, rec := range capture.records(t) {
		assert.NotEqual(t, "ERROR", rec["level"], "a returned error must not also be logged as an error")
	}
}

func TestClient_FailedRequestWithRetries_DoesNotLeakQueryString(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	serverURL := server.URL
	server.Close() // connection refused: http.Client.Do returns a *url.Error carrying the full URL

	capture, logger := newLogCapture()
	client := NewClient(serverURL, WithLogger(logger))

	err := client.Request(context.Background(), Request{
		Method: http.MethodGet,
		Path:   "/down?token=path-secret",
		Query:  url.Values{"key": {"query-secret"}},
		Retry: &RetryConfig{
			MaxRetries:     1,
			InitialBackoff: time.Millisecond,
			MaxBackoff:     time.Millisecond,
		},
	}, nil)
	require.ErrorIs(t, err, ErrRequestFailed)

	require.Len(t, capture.find(t, "remote: request failed"), 2)
	retries := capture.find(t, "remote: retrying request")
	require.Len(t, retries, 1)
	assert.NotEmpty(t, retries[0]["error"], "the cause must still be logged, just without the URL")

	assert.NotContains(t, capture.raw(), "secret", "neither the debug nor the retry record may carry the query string")
	assert.NotContains(t, err.Error(), "secret", "the returned error is logged by the caller, so it must not carry the query string either")

	// The URL is redacted, not dropped: the error keeps its type, host and path.
	var ue *url.Error
	require.ErrorAs(t, err, &ue)
	assert.Equal(t, serverURL+"/down", ue.URL)
	assert.Contains(t, retries[0]["error"], serverURL+"/down")
}

func TestClient_InvalidURL_DoesNotLeakQueryString(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer server.Close()

	for name, path := range map[string]string{
		"relative": "/bad%zz?token=parse-secret",
		"absolute": server.URL + "/bad%zz?token=parse-secret",
	} {
		t.Run(name, func(t *testing.T) {
			capture, logger := newLogCapture()
			client := NewClient(server.URL, WithLogger(logger))

			err := client.Request(context.Background(), Request{Method: http.MethodGet, Path: path}, nil)
			require.Error(t, err)

			assert.Contains(t, err.Error(), `invalid URL escape "%zz"`, "the cause must still be reported, just without the URL")
			assert.NotContains(t, err.Error(), "secret", "the returned error is logged by the caller, so it must not carry the query string")
			assert.NotContains(t, capture.raw(), "secret")
		})
	}
	assert.Zero(t, atomic.LoadInt32(&hits), "a request with an invalid URL must not be sent")
}

func TestRedactURL(t *testing.T) {
	cause := errors.New("connection refused")

	tests := []struct {
		name    string
		url     string
		wantURL string
	}{
		{"query", "https://api.example.com/v1/items?token=secret", "https://api.example.com/v1/items"},
		{"empty query", "https://api.example.com/v1/items?", "https://api.example.com/v1/items"},
		{"fragment", "https://api.example.com/v1/items#secret", "https://api.example.com/v1/items"},
		{"userinfo", "https://user:secret@api.example.com/v1", "https://api.example.com/v1"},
		{"no query", "https://api.example.com/v1/items", "https://api.example.com/v1/items"},
		{"unparseable", "https://api.example.com/%zz?token=secret", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := redactURL(fmt.Errorf("wrapped: %w", &url.Error{Op: "Get", URL: tt.url, Err: cause}))

			var ue *url.Error
			require.ErrorAs(t, err, &ue)
			assert.Equal(t, "Get", ue.Op)
			assert.Equal(t, tt.wantURL, ue.URL)
			assert.ErrorIs(t, err, cause)
			assert.NotContains(t, err.Error(), "secret")
		})
	}

	t.Run("non-URL error passes through", func(t *testing.T) {
		assert.Same(t, cause, redactURL(cause))
	})
}

func TestClient_RetryIsLoggedAtInfoWithResendCount(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	capture, logger := newLogCapture()
	client := NewClient(server.URL, WithLogger(logger))

	err := client.Request(context.Background(), Request{
		Method: http.MethodGet,
		Path:   "/retry",
		Retry: &RetryConfig{
			MaxRetries:      2,
			InitialBackoff:  time.Millisecond,
			MaxBackoff:      time.Millisecond,
			RetryableStatus: []int{http.StatusServiceUnavailable},
		},
	}, nil)
	require.NoError(t, err)

	retries := capture.find(t, "remote: retrying request")
	require.Len(t, retries, 1)
	assert.Equal(t, "INFO", retries[0]["level"])
	assert.EqualValues(t, 1, retries[0][attrResendCount])
	assert.EqualValues(t, http.StatusServiceUnavailable, retries[0][attrStatusCode])
	assert.Equal(t, "/retry", retries[0][attrURLPath])

	started := capture.find(t, "remote: request started")
	require.Len(t, started, 2)
	assert.NotContains(t, started[0], attrResendCount)
	assert.EqualValues(t, 1, started[1][attrResendCount])
}

func TestManager_WithManagerLogger_TagsClientLogsWithService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	capture, logger := newLogCapture()
	m := NewManager(WithManagerLogger(logger))
	require.NoError(t, m.SetServices(ServiceConfig{ID: "svc", URL: server.URL}))

	loaded := capture.find(t, "remote: services loaded")
	require.Len(t, loaded, 1)
	assert.Equal(t, "INFO", loaded[0]["level"])
	assert.EqualValues(t, 1, loaded[0]["count"])

	require.NoError(t, m.Call(context.Background(), "svc", Request{Method: http.MethodGet, Path: "/ping"}, nil))

	completed := capture.find(t, "remote: request completed")
	require.Len(t, completed, 1)
	assert.Equal(t, "svc", completed[0][attrService])
}

func TestManager_WithManagerLogger_ReachesAuthenticator(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer tokenServer.Close()

	capture, logger := newLogCapture()
	m := NewManager(WithManagerLogger(logger))
	require.NoError(t, m.SetServices(ServiceConfig{
		ID:  "secured",
		URL: "http://unused.local",
		Auth: &auth.Config{
			Type:   auth.TypeOAuth2,
			OAuth2: &auth.OAuth2Config{TokenURL: tokenServer.URL, ClientID: "id", ClientSecret: "secret"},
		},
	}))

	err := m.Call(context.Background(), "secured", Request{Method: http.MethodGet, Path: "/x"}, nil)
	require.Error(t, err)

	failed := capture.find(t, "remote/auth: oauth2 token fetch failed")
	require.Len(t, failed, 1)
	assert.Equal(t, "WARN", failed[0]["level"])
	assert.Equal(t, "secured", failed[0][attrService])
}
