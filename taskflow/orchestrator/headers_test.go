// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator_test

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

	"github.com/OpenNSW/core/remote"
	"github.com/OpenNSW/core/taskflow/orchestrator"
)

// The end-to-end shape of the feature: an artifact maps a workflow variable onto
// a header name, and it arrives at the service on a request the plugin built
// without knowing the header exists.
func TestContextWithInputHeaders_ReachTheService(t *testing.T) {
	var seen http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	body := fmt.Sprintf(`{"version":"1.0","services":[{"id":"svc","url":%q}]}`, server.URL)
	path := filepath.Join(t.TempDir(), "services.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	manager := remote.NewManager()
	require.NoError(t, manager.LoadServices(path))

	// What the engine hands a plugin after the artifact's input_mapping ran.
	inputs := map[string]any{
		"payload": map[string]any{"document": "x"},
		orchestrator.HeadersInputKey: map[string]any{
			"x-partner-client-key": "issued-key-1",
		},
	}

	ctx, err := orchestrator.ContextWithInputHeaders(context.Background(), inputs)
	require.NoError(t, err)

	// A plugin that models only a body still sends the header.
	require.NoError(t, manager.Call(ctx, "svc", remote.Request{
		Method: "POST", Path: "/submit", Body: inputs["payload"],
	}, nil))

	assert.Equal(t, "issued-key-1", seen.Get("X-Partner-Client-Key"))
}

func TestContextWithInputHeaders_NothingToCarry(t *testing.T) {
	base := context.Background()

	for name, inputs := range map[string]map[string]any{
		"no reserved key":  {"payload": "x"},
		"explicitly null":  {orchestrator.HeadersInputKey: nil},
		"nil inputs":       nil,
		"no headers in it": {orchestrator.HeadersInputKey: map[string]any{}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := orchestrator.ContextWithInputHeaders(base, inputs)
			require.NoError(t, err)
			assert.Equal(t, base, ctx, "the context is returned unchanged")
		})
	}
}

// An artifact naming a header the engine cannot pass is a wiring mistake. It
// fails here rather than reaching the service without it, where the symptom
// would be the provider's rejection.
func TestContextWithInputHeaders_MalformedIsAnError(t *testing.T) {
	// The shape of the reserved input is this package's own business.
	t.Run("the input is not a map", func(t *testing.T) {
		ctx, err := orchestrator.ContextWithInputHeaders(context.Background(), map[string]any{
			orchestrator.HeadersInputKey: "x-key: value",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `task input "_headers" must be a map`)
		assert.Equal(t, context.Background(), ctx)
	})

	// What counts as a sendable header is remote's; the reserved key names the
	// failure so an artifact author knows where to look. See
	// remote.ContextWithHeaderValues for the full table.
	t.Run("an entry remote refuses is wrapped", func(t *testing.T) {
		_, err := orchestrator.ContextWithInputHeaders(context.Background(), map[string]any{
			orchestrator.HeadersInputKey: map[string]any{"x-count": 7},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `task input "_headers"`)
		assert.Contains(t, err.Error(), `header "x-count" must be a string`)
	})
}

// An optional mapping whose source was blank must not fail the task: the call
// goes out without that header and the service answers for itself.
func TestContextWithInputHeaders_EmptyValueIsDropped(t *testing.T) {
	ctx, err := orchestrator.ContextWithInputHeaders(context.Background(), map[string]any{
		orchestrator.HeadersInputKey: map[string]any{"x-optional": "", "x-present": "v"},
	})
	require.NoError(t, err)

	var seen http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	require.NoError(t, remote.NewClient(server.URL).JSONRequest(ctx, remote.Request{Method: "GET", Path: "/"}, nil))
	assert.Empty(t, seen.Get("X-Optional"))
	assert.Equal(t, "v", seen.Get("X-Present"))
}
