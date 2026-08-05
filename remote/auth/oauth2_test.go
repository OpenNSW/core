// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
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

		auth := NewOAuth2(ts.URL, "", "", nil)
		req, _ := http.NewRequest(http.MethodGet, "http://local", nil)

		err := auth.Apply(req)
		assert.NoError(t, err)
		assert.Equal(t, "Bearer valid-token", req.Header.Get("Authorization"))
	})

	t.Run("error", func(t *testing.T) {
		// Providing an invalid URL to trigger getToken error
		auth := NewOAuth2("cache-busting-invalid-url", "", "", nil)
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

	auth := NewOAuth2(ts.URL, "client-1", "secret-1", nil)

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

	auth.tokenURL = ts.URL

	t1, err := auth.getToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "new-token", t1)
}

func TestOAuth2_Errors(t *testing.T) {
	t.Run("invalid token url", func(t *testing.T) {
		auth := NewOAuth2("http://invalid-dns-name-xyz.local", "", "", nil)
		_, err := auth.getToken(context.Background())
		assert.Error(t, err)
	})

	t.Run("request creation failure", func(t *testing.T) {
		// A URL with a control character will cause http.NewRequest to fail
		auth := NewOAuth2("http://example.com/\x7f", "", "", nil)
		_, err := auth.getToken(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create token request")
	})

	t.Run("server error 500", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		auth := NewOAuth2(ts.URL, "", "", nil)
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

		auth := NewOAuth2(ts.URL, "", "", nil)
		_, err := auth.getToken(context.Background())
		assert.Error(t, err)
	})

	t.Run("missing access token", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"expires_in": 3600}`))
		}))
		defer ts.Close()

		auth := NewOAuth2(ts.URL, "", "", nil)
		_, err := auth.getToken(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no access token")
	})
}

func TestOAuth2_InsecureSkipTLSVerify(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token": "tls-token", "expires_in": 3600}`))
	}))
	defer ts.Close()

	auth := NewOAuth2(ts.URL, "", "", nil)
	auth.SetInsecureSkipTLSVerify(true)

	token, err := auth.getToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "tls-token", token)
}

func TestOAuth2_InsecureSkipTLSVerify_OffByDefault(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token": "tls-token", "expires_in": 3600}`))
	}))
	defer ts.Close()

	auth := NewOAuth2(ts.URL, "", "", nil)
	_, err := auth.getToken(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token request failed")
	assert.NotContains(t, err.Error(), "tls-token")
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

	auth := NewOAuth2(ts.URL, "", "", []string{"read", "write"})

	token, err := auth.getToken(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "token-with-scopes", token)
}

func TestOAuth2_UsesBasicAuth(t *testing.T) {
	var gotUser, gotPass, gotBodySecret string
	var basicOK bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, basicOK = r.BasicAuth()
		_ = r.ParseForm()
		gotBodySecret = r.Form.Get("client_secret")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token": "t", "expires_in": 3600}`))
	}))
	defer ts.Close()

	auth := NewOAuth2(ts.URL, "my-client", "my-secret", nil)
	_, err := auth.getToken(context.Background())
	require.NoError(t, err)

	assert.True(t, basicOK, "client credentials must be sent in the Authorization header")
	assert.Equal(t, "my-client", gotUser)
	assert.Equal(t, "my-secret", gotPass)
	assert.Empty(t, gotBodySecret, "client_secret must not be sent in the request body")
}

func TestOAuth2_ContextCancel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	auth := NewOAuth2(ts.URL, "", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := auth.getToken(ctx)
	assert.Error(t, err)
}

// TestOAuth2_EndpointParams asserts extra parameters reach the token request BODY, per
// RFC 6749 §3.2.
//
// The body is read raw rather than via r.Form on purpose: r.Form merges the URL query into
// the form, so an r.Form.Get("resource") assertion would also pass if the parameter were
// appended to token_url as a query string — the practice this feature replaces. Only a
// raw-body assertion tells the two apart.
func TestOAuth2_EndpointParams(t *testing.T) {
	var gotBody, gotRawQuery string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		gotBody = string(raw)
		gotRawQuery = r.URL.RawQuery

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token": "bound-token", "expires_in": 3600}`))
	}))
	defer ts.Close()

	auth := NewOAuth2(ts.URL, "", "", []string{"read"}, WithEndpointParams(url.Values{
		"resource": {"https://api.example"},
		"audience": {"example-api"},
	}))

	token, err := auth.getToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "bound-token", token)

	form, err := url.ParseQuery(gotBody)
	require.NoError(t, err)
	assert.Equal(t, "https://api.example", form.Get("resource"))
	assert.Equal(t, "example-api", form.Get("audience"))
	// The flow's own parameters must survive alongside them.
	assert.Equal(t, "client_credentials", form.Get("grant_type"))
	assert.Equal(t, "read", form.Get("scope"))
	assert.Empty(t, gotRawQuery, "parameters belong in the body, not on the token URL")
}

func TestOAuth2_EndpointParams_Repeated(t *testing.T) {
	var gotBody string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token": "t", "expires_in": 3600}`))
	}))
	defer ts.Close()

	auth := NewOAuth2(ts.URL, "", "", nil, WithEndpointParams(url.Values{
		"resource": {"https://a.example", "https://b.example"},
	}))

	_, err := auth.getToken(context.Background())
	require.NoError(t, err)

	form, err := url.ParseQuery(gotBody)
	require.NoError(t, err)
	assert.Equal(t, []string{"https://a.example", "https://b.example"}, form["resource"])
}

func TestOAuth2_EndpointParams_OmittedByDefault(t *testing.T) {
	var gotBody string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token": "t", "expires_in": 3600}`))
	}))
	defer ts.Close()

	auth := NewOAuth2(ts.URL, "", "", []string{"read"})

	_, err := auth.getToken(context.Background())
	require.NoError(t, err)

	form, err := url.ParseQuery(gotBody)
	require.NoError(t, err)
	assert.Equal(t, []string{"grant_type", "scope"}, sortedKeys(form))
}

func TestOAuth2_EndpointParams_RejectsReserved(t *testing.T) {
	for _, key := range []string{"grant_type", "scope", "client_id", "client_secret"} {
		t.Run(key, func(t *testing.T) {
			// Programmatic path: rejected when the token request is built.
			auth := NewOAuth2("https://idp.example/token", "", "", nil,
				WithEndpointParams(url.Values{key: {"x"}}))
			_, err := auth.getToken(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), key)

			// Config path: rejected at build time, before any request is made.
			options := []byte(`{"token_url":"https://idp.example/token","client_id":"c",` +
				`"client_secret":"s","endpoint_params":{"` + key + `":"x"}}`)
			_, err = Build("oauth2", options)
			require.Error(t, err)
			assert.Contains(t, err.Error(), key)
		})
	}
}

func TestEndpointParams_UnmarshalJSON(t *testing.T) {
	t.Run("scalar becomes a single-element slice", func(t *testing.T) {
		var p EndpointParams
		require.NoError(t, json.Unmarshal([]byte(`{"resource":"https://api.example"}`), &p))
		assert.Equal(t, EndpointParams{"resource": {"https://api.example"}}, p)
	})

	t.Run("array is preserved", func(t *testing.T) {
		var p EndpointParams
		require.NoError(t, json.Unmarshal([]byte(`{"resource":["a","b"]}`), &p))
		assert.Equal(t, EndpointParams{"resource": {"a", "b"}}, p)
	})

	t.Run("rejects a non-string value", func(t *testing.T) {
		var p EndpointParams
		err := json.Unmarshal([]byte(`{"resource":42}`), &p)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be a string or an array of strings")
	})
}

// TestOAuth2_ErrorResponse_IncludesOAuthError: a rejection must say why, not just how.
func TestOAuth2_ErrorResponse_IncludesOAuthError(t *testing.T) {
	t.Run("error and description", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_target",` +
				`"error_description":"The resource parameter does not match any registered resource server"}`))
		}))
		defer ts.Close()

		_, err := NewOAuth2(ts.URL, "", "", nil).getToken(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status 400")
		assert.Contains(t, err.Error(), "invalid_target")
		assert.Contains(t, err.Error(), "does not match any registered resource server")
	})

	t.Run("non-OAuth2 body still reports the status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<html>gateway error</html>`))
		}))
		defer ts.Close()

		_, err := NewOAuth2(ts.URL, "", "", nil).getToken(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status 500")
	})
}

func sortedKeys(v url.Values) []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
