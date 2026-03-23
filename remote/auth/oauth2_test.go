package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuth2_Apply(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"access_token": "valid-token", "expires_in": 3600}`))
		}))
		defer ts.Close()

		auth := NewOAuth2(OAuth2Config{TokenURL: ts.URL})
		req, _ := http.NewRequest(http.MethodGet, "http://local", nil)

		err := auth.Apply(req)
		assert.NoError(t, err)
		assert.Equal(t, "Bearer valid-token", req.Header.Get("Authorization"))
	})

	t.Run("error", func(t *testing.T) {
		// Providing an invalid URL to trigger getToken error
		auth := NewOAuth2(OAuth2Config{TokenURL: "cache-busting-invalid-url"})
		req, _ := http.NewRequest(http.MethodGet, "http://local", nil)

		err := auth.Apply(req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "oauth2 failed")
	})
}

func TestOAuth2_getToken_Caching(t *testing.T) {
	var callCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token": "token-1", "expires_in": 3600}`))
	}))
	defer ts.Close()

	auth := NewOAuth2(OAuth2Config{
		TokenURL:     ts.URL,
		ClientID:     "client-1",
		ClientSecret: "secret-1",
	})

	// First call
	t1, err := auth.getToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "token-1", t1)
	assert.Equal(t, 1, callCount)

	// Second call - should be cached
	t2, err := auth.getToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "token-1", t2)
	assert.Equal(t, 1, callCount)
}

func TestOAuth2_ExpiryBuffer(t *testing.T) {
	// Set an expired token
	auth := &OAuth2{
		accessToken: "old-token",
		expiry:      time.Now().Add(-10 * time.Minute),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token": "new-token", "expires_in": 3600}`))
	}))
	defer ts.Close()

	auth.cfg.TokenURL = ts.URL

	t1, err := auth.getToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "new-token", t1)
}

func TestOAuth2_Errors(t *testing.T) {
	t.Run("invalid token url", func(t *testing.T) {
		auth := NewOAuth2(OAuth2Config{TokenURL: "http://invalid-dns-name-xyz.local"})
		_, err := auth.getToken(context.Background())
		assert.Error(t, err)
	})

	t.Run("request creation failure", func(t *testing.T) {
		// A URL with a control character will cause http.NewRequest to fail
		auth := NewOAuth2(OAuth2Config{TokenURL: "http://example.com/\x7f"})
		_, err := auth.getToken(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create token request")
	})

	t.Run("server error 500", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		auth := NewOAuth2(OAuth2Config{TokenURL: ts.URL})
		_, err := auth.getToken(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "status 500")
	})

	t.Run("invalid json response", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{ "broken": json }`))
		}))
		defer ts.Close()

		auth := NewOAuth2(OAuth2Config{TokenURL: ts.URL})
		_, err := auth.getToken(context.Background())
		assert.Error(t, err)
	})

	t.Run("missing access token", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"expires_in": 3600}`))
		}))
		defer ts.Close()

		auth := NewOAuth2(OAuth2Config{TokenURL: ts.URL})
		_, err := auth.getToken(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no access token")
	})
}

func TestOAuth2_Scopes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseForm()
		assert.NoError(t, err)
		assert.Equal(t, "read write", r.Form.Get("scope"))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token": "token-with-scopes", "expires_in": 3600}`))
	}))
	defer ts.Close()

	auth := NewOAuth2(OAuth2Config{
		TokenURL: ts.URL,
		Scopes:   []string{"read", "write"},
	})

	token, err := auth.getToken(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "token-with-scopes", token)
}

func TestOAuth2_ContextCancel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	auth := NewOAuth2(OAuth2Config{TokenURL: ts.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := auth.getToken(ctx)
	assert.Error(t, err)
}
