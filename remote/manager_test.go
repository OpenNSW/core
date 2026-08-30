// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_LoadServices_Errors(t *testing.T) {
	manager := NewManager()

	t.Run("file not found", func(t *testing.T) {
		err := manager.LoadServices("non-existent.json")
		assert.Error(t, err)
	})

	t.Run("invalid json", func(t *testing.T) {
		tmpFile, _ := os.CreateTemp("", "invalid-*.json")
		defer os.Remove(tmpFile.Name())
		_, _ = tmpFile.WriteString(`{ "invalid": "json" `)
		_ = tmpFile.Close()

		err := manager.LoadServices(tmpFile.Name())
		assert.Error(t, err)
	})
}

func TestManager_LoadServices_Reload(t *testing.T) {
	manager := NewManager()

	// 1. Initial Load
	config1 := `{"version":"1.0","services":[{"id":"s1","url":"http://s1"}]}`
	tmpFile, _ := os.CreateTemp("", "conf-*.json")
	defer os.Remove(tmpFile.Name())
	_, _ = tmpFile.WriteString(config1)
	_ = tmpFile.Close()

	err := manager.LoadServices(tmpFile.Name())
	assert.NoError(t, err)
	assert.Contains(t, manager.ListServices(), "s1")

	// Warm up cache
	_, _ = manager.GetClient("s1")
	manager.mu.RLock()
	assert.Len(t, manager.clients, 1)
	manager.mu.RUnlock()

	// 2. Reload with different config
	config2 := `{"version":"2.0","services":[{"id":"s2","url":"http://s2"}]}`
	_ = os.WriteFile(tmpFile.Name(), []byte(config2), 0644)

	err = manager.LoadServices(tmpFile.Name())
	assert.NoError(t, err)

	// Verify old service is gone and cache is wiped
	assert.NotContains(t, manager.ListServices(), "s1")
	assert.Contains(t, manager.ListServices(), "s2")
	manager.mu.RLock()
	assert.Len(t, manager.clients, 0)
	manager.mu.RUnlock()
}

func TestManager_LoadServices_AttachesAuthenticator(t *testing.T) {
	// A service with valid auth loads, and its authenticator is attached to the
	// client built by GetClient. (The auth-type matrix itself is covered by
	// auth.Build's tests.)
	body := `{"version":"1.0","services":[{"id":"svc","url":"http://local",` +
		`"auth":{"type":"bearer","options":{"token":"my-token"}}}]}`
	path := filepath.Join(t.TempDir(), "services.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	manager := NewManager()
	require.NoError(t, manager.LoadServices(path))

	client, err := manager.GetClient("svc")
	require.NoError(t, err)
	assert.NotNil(t, client.authenticator)
}

func TestManager_LoadServices_UnsupportedAuthType(t *testing.T) {
	body := `{"version":"1.0","services":[{"id":"svc","url":"http://local",` +
		`"auth":{"type":"biometric","options":{}}}]}`
	path := filepath.Join(t.TempDir(), "services.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	manager := NewManager()
	err := manager.LoadServices(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported auth type")
}

func TestManager_GetClient_NoAuth(t *testing.T) {
	manager := NewManager()
	manager.configs["no-auth"] = ServiceConfig{ID: "no-auth", URL: "http://local"}

	client, err := manager.GetClient("no-auth")
	assert.NoError(t, err)
	assert.Nil(t, client.authenticator)
}

func TestManager_GetClient_TimeoutError(t *testing.T) {
	manager := NewManager()
	manager.configs["timeout-service"] = ServiceConfig{
		ID:      "timeout-service",
		URL:     "http://local",
		Timeout: "invalid-duration",
	}

	_, err := manager.GetClient("timeout-service")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid timeout")
}

func TestManager_Concurrency(t *testing.T) {
	manager := NewManager()
	manager.configs["s1"] = ServiceConfig{ID: "s1", URL: "http://localhost"}

	var wg sync.WaitGroup
	const workers = 20

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_, _ = manager.GetClient("s1")
			_ = manager.ListServices()
		}()
	}
	wg.Wait()
}

func TestManager_ListServices(t *testing.T) {
	manager := NewManager()
	manager.configs["s1"] = ServiceConfig{ID: "s1", URL: "http://local"}
	manager.configs["s2"] = ServiceConfig{ID: "s2", URL: "http://local"}

	list := manager.ListServices()
	assert.Len(t, list, 2)
	assert.Contains(t, list, "s1")
	assert.Contains(t, list, "s2")
}

func TestManager_Call_Integration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	manager := NewManager()
	manager.configs["test"] = ServiceConfig{ID: "test", URL: server.URL}

	err := manager.Call(context.Background(), "test", Request{Method: "GET", Path: "/"}, nil)
	assert.NoError(t, err)
}

func TestManager_Call_EmptyServiceIDIsNotResolvedFromURL(t *testing.T) {
	// Call no longer infers a service from an absolute Request.Path when
	// serviceID is empty — it must fail the normal registry lookup instead.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("the server must never be reached: an empty serviceID must fail before any request is sent")
	}))
	defer server.Close()

	manager := NewManager()
	manager.configs["test"] = ServiceConfig{ID: "test", URL: server.URL}

	err := manager.Call(context.Background(), "", Request{Method: "GET", Path: server.URL + "/"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `service "" not found`)
}
