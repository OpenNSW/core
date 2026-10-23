// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OpenNSW/core/remote/auth"
)

// bearerService returns a single bearer-auth service with the given token.
func bearerService(url, token string) ServiceConfig {
	return ServiceConfig{
		ID:   "svc",
		URL:  url,
		Auth: &auth.Config{Type: auth.TypeBearer, Bearer: &auth.BearerConfig{Token: token}},
	}
}

func TestManager_SetServices_SendsTokenVerbatim(t *testing.T) {
	// The manager never resolves secrets: a token shaped like an env reference
	// is sent as written, even when that env var is set.
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	t.Setenv("SVC_TOKEN", "resolved-token")
	manager := NewManager()
	require.NoError(t, manager.SetServices(bearerService(server.URL, "env:SVC_TOKEN")))

	err := manager.Call(context.Background(), "svc", Request{Method: "GET", Path: "/"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer env:SVC_TOKEN", gotAuth)
}

func TestManager_SetServices_FailedReloadKeepsPreviousState(t *testing.T) {
	// A first set succeeds.
	manager := NewManager()
	require.NoError(t, manager.SetServices(bearerService("http://local", "plain-token")))
	require.Contains(t, manager.ListServices(), "svc")

	// A second set that fails (auth type with no settings) must leave the
	// manager's existing state untouched, not corrupted/half-applied.
	err := manager.SetServices(
		ServiceConfig{ID: "other", URL: "http://other"},
		ServiceConfig{ID: "bad", URL: "http://bad", Auth: &auth.Config{Type: auth.TypeBearer}},
	)
	require.Error(t, err)

	// The previously set service is still the only one, and still usable.
	assert.Equal(t, []string{"svc"}, manager.ListServices())
	client, err := manager.GetClient("svc")
	require.NoError(t, err)
	assert.NotNil(t, client.authenticator)
}
