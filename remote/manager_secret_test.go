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

// writeServices writes a services.json with a single bearer-auth service whose
// token is the given (possibly scheme-prefixed) reference, and returns its path.
func writeServices(t *testing.T, url, tokenRef string) string {
	t.Helper()
	body := fmt.Sprintf(
		`{"version":"1.0","services":[{"id":"svc","url":%q,"auth":{"type":"bearer","options":{"token":%q}}}]}`,
		url, tokenRef,
	)
	path := filepath.Join(t.TempDir(), "services.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestManager_LoadServices_ResolvesFileSecretAtStartup(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	secretPath := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(secretPath, []byte("resolved-token"), 0o600))

	path := writeServices(t, server.URL, "file:"+secretPath)

	manager := NewManager()
	require.NoError(t, manager.LoadServices(path))

	err := manager.Call(context.Background(), "svc", Request{Method: "GET", Path: "/"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer resolved-token", gotAuth)
}

func TestManager_LoadServices_FailsLoudOnMissingFile(t *testing.T) {
	path := writeServices(t, "http://local", "file:/definitely/does/not/exist/token")

	manager := NewManager()
	err := manager.LoadServices(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read secret file")
}

func TestManager_LoadServices_LiteralTokenStillWorks(t *testing.T) {
	// Backward compatibility: a plain literal token resolves to itself.
	path := writeServices(t, "http://local", "plain-token")

	manager := NewManager()
	require.NoError(t, manager.LoadServices(path))
}

func TestManager_LoadServices_FailedReloadKeepsPreviousState(t *testing.T) {
	// A first load succeeds.
	good := writeServices(t, "http://local", "plain-token")
	manager := NewManager()
	require.NoError(t, manager.LoadServices(good))
	require.Contains(t, manager.ListServices(), "svc")

	// A second load that fails (unresolvable reference) must leave the manager's
	// existing state untouched, not corrupted/half-applied.
	bad := writeServices(t, "http://other", "file:/definitely/does/not/exist/token")
	err := manager.LoadServices(bad)
	require.Error(t, err)

	// The previously loaded service is still usable.
	assert.Contains(t, manager.ListServices(), "svc")
	client, err := manager.GetClient("svc")
	require.NoError(t, err)
	assert.NotNil(t, client.authenticator)
}
