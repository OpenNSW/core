// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuild(t *testing.T) {
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
		},
		{
			name:     "bearer success",
			authType: "bearer",
			options:  map[string]string{"token": "my-token"},
		},
		{
			name:     "oauth2 success",
			authType: "oauth2",
			options: map[string]any{
				"token_url":                "http://auth",
				"client_id":                "id",
				"client_secret":            "secret",
				"insecure_skip_tls_verify": true,
			},
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
			optsJSON, err := json.Marshal(tt.options)
			require.NoError(t, err)

			authn, err := Build(tt.authType, optsJSON)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, authn)
		})
	}
}

func TestBuild_ResolvesSecretReference(t *testing.T) {
	t.Setenv("BUILD_TOKEN", "resolved")
	authn, err := Build("bearer", json.RawMessage(`{"token":"env:BUILD_TOKEN"}`))
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodGet, "http://local", nil)
	require.NoError(t, authn.Apply(req))
	assert.Equal(t, "Bearer resolved", req.Header.Get("Authorization"))
}

func TestBuild_FailsLoudOnUnsetEnv(t *testing.T) {
	_, err := Build("bearer", json.RawMessage(`{"token":"env:DEFINITELY_UNSET"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not set or is empty")
}

func TestBuild_MissingOptions(t *testing.T) {
	for _, opts := range []json.RawMessage{nil, json.RawMessage("null")} {
		_, err := Build("bearer", opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing options")
	}
}
