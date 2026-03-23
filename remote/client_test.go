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
