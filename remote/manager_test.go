package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestManager_GetClient_AuthTypes(t *testing.T) {
	manager := NewManager()

	tests := []struct {
		name     string
		authType string
		options  any
		wantErr  bool
	}{
		{
			name:     "api_key success",
			authType: "api_key",
			options:  map[string]string{"key": "X-API", "value": "secret"},
			wantErr:  false,
		},
		{
			name:     "bearer success",
			authType: "bearer",
			options:  map[string]string{"token": "my-token"},
			wantErr:  false,
		},
		{
			name:     "oauth2 success",
			authType: "oauth2",
			options: map[string]any{
				"token_url":     "http://auth",
				"client_id":     "id",
				"client_secret": "secret",
			},
			wantErr: false,
		},
		{
			name:     "unsupported type",
			authType: "biometric",
			options:  map[string]string{"fingerprint": "xyz"},
			wantErr:  true,
		},
		{
			name:     "invalid api_key options",
			authType: "api_key",
			options:  "not-a-map",
			wantErr:  true,
		},
		{
			name:     "invalid bearer options",
			authType: "bearer",
			options:  "not-a-map",
			wantErr:  true,
		},
		{
			name:     "invalid oauth2 options",
			authType: "oauth2",
			options:  "not-a-map",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optsJSON, _ := json.Marshal(tt.options)
			cfg := ServiceConfig{
				ID:  tt.name,
				URL: "http://local",
				Auth: &AuthConfig{
					Type:    tt.authType,
					Options: optsJSON,
				},
			}
			manager.configs[tt.name] = cfg

			client, err := manager.GetClient(tt.name)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, client)
			}
		})
	}
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
