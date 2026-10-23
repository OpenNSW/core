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
	"github.com/OpenNSW/core/secret"
)

// bearerService returns a single bearer-auth service whose token is the given
// (possibly scheme-prefixed) reference.
func bearerService(url, tokenRef string) ServiceConfig {
	return ServiceConfig{
		ID:   "svc",
		URL:  url,
		Auth: &auth.Config{Type: auth.TypeBearer, Bearer: &auth.BearerConfig{Token: secret.SecretRef(tokenRef)}},
	}
}

func TestManager_SetServices_ResolvesEnvSecretAtStartup(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	t.Setenv("SVC_TOKEN", "resolved-token")
	svc := bearerService(server.URL, "env:SVC_TOKEN")

	manager := NewManager()
	require.NoError(t, manager.SetServices(svc))

	err := manager.Call(context.Background(), "svc", Request{Method: "GET", Path: "/"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer resolved-token", gotAuth)
}

func TestManager_SetServices_FailsLoudOnUnsetEnv(t *testing.T) {
	svc := bearerService("http://local", "env:DEFINITELY_UNSET_TOKEN")

	manager := NewManager()
	err := manager.SetServices(svc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to configure auth for service")
}

func TestManager_SetServices_LiteralTokenStillWorks(t *testing.T) {
	// Backward compatibility: a plain literal token resolves to itself.
	svc := bearerService("http://local", "plain-token")

	manager := NewManager()
	require.NoError(t, manager.SetServices(svc))
}

func TestManager_SetServices_FailedReloadKeepsPreviousState(t *testing.T) {
	// A first set succeeds.
	good := bearerService("http://local", "plain-token")
	manager := NewManager()
	require.NoError(t, manager.SetServices(good))
	require.Contains(t, manager.ListServices(), "svc")

	// A second set that fails (unresolvable reference) must leave the manager's
	// existing state untouched, not corrupted/half-applied.
	bad := bearerService("http://other", "env:DEFINITELY_UNSET_TOKEN")
	err := manager.SetServices(bad)
	require.Error(t, err)

	// The previously set service is still usable.
	assert.Contains(t, manager.ListServices(), "svc")
	client, err := manager.GetClient("svc")
	require.NoError(t, err)
	assert.NotNil(t, client.authenticator)
}
