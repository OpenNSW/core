// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/OpenNSW/core/remote/auth"
)

func TestManager_SetServices_Replaces(t *testing.T) {
	manager := NewManager()

	// 1. Initial set
	require.NoError(t, manager.SetServices(ServiceConfig{ID: "s1", URL: "http://s1"}))
	assert.Contains(t, manager.ListServices(), "s1")

	// Warm up cache
	_, _ = manager.GetClient("s1")
	manager.mu.RLock()
	assert.Len(t, manager.clients, 1)
	manager.mu.RUnlock()

	// 2. Replace with a different set
	require.NoError(t, manager.SetServices(ServiceConfig{ID: "s2", URL: "http://s2"}))

	// Verify old service is gone and cache is wiped
	assert.NotContains(t, manager.ListServices(), "s1")
	assert.Contains(t, manager.ListServices(), "s2")
	manager.mu.RLock()
	assert.Len(t, manager.clients, 0)
	manager.mu.RUnlock()
}

func TestManager_SetServices_AttachesAuthenticator(t *testing.T) {
	// A service with valid auth is set, and its authenticator is attached to the
	// client built by GetClient. (The auth-type matrix itself is covered by
	// auth.Build's tests.)
	manager := NewManager()
	require.NoError(t, manager.SetServices(ServiceConfig{
		ID:   "svc",
		URL:  "http://local",
		Auth: &auth.Config{Type: auth.TypeBearer, Bearer: &auth.BearerConfig{Token: "my-token"}},
	}))

	client, err := manager.GetClient("svc")
	require.NoError(t, err)
	assert.NotNil(t, client.authenticator)
}

func TestManager_SetServices_UnsupportedAuthType(t *testing.T) {
	manager := NewManager()
	err := manager.SetServices(ServiceConfig{ID: "svc", URL: "http://local", Auth: &auth.Config{Type: "biometric"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported auth type")
}

func TestServiceConfig_DecodesFromJSONAndYAML(t *testing.T) {
	// The application owns where its configuration lives; the same services
	// written in either format decode to the same []ServiceConfig.
	const jsonDoc = `{"services": [{
		"id": "npqs-api",
		"url": "https://npqs.example/api",
		"timeout": "30s",
		"auth": {"type": "bearer", "bearer": {"token": "env:NPQS_TOKEN"}},
		"tls": {"clientCertFile": "/certs/client.crt", "clientKeyFile": "/certs/client.key"}
	}]}`
	const yamlDoc = `
services:
  - id: npqs-api
    url: https://npqs.example/api
    timeout: 30s
    auth:
      type: bearer
      bearer:
        token: env:NPQS_TOKEN
    tls:
      clientCertFile: /certs/client.crt
      clientKeyFile: /certs/client.key
`
	want := []ServiceConfig{{
		ID:      "npqs-api",
		URL:     "https://npqs.example/api",
		Timeout: "30s",
		Auth:    &auth.Config{Type: auth.TypeBearer, Bearer: &auth.BearerConfig{Token: "env:NPQS_TOKEN"}},
		TLS:     &TLSSettings{ClientCertFile: "/certs/client.crt", ClientKeyFile: "/certs/client.key"},
	}}

	type appConfig struct {
		Services []ServiceConfig `json:"services" yaml:"services"`
	}

	var fromJSON appConfig
	require.NoError(t, json.Unmarshal([]byte(jsonDoc), &fromJSON))
	assert.Equal(t, want, fromJSON.Services)

	var fromYAML appConfig
	require.NoError(t, yaml.Unmarshal([]byte(yamlDoc), &fromYAML))
	assert.Equal(t, want, fromYAML.Services)
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
