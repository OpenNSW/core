// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestBuild_MissingOptions(t *testing.T) {
	for _, opts := range []json.RawMessage{nil, json.RawMessage("null")} {
		_, err := Build("bearer", opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing options")
	}
}

func TestBuild_ResolvesFileSecretRef(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(secretPath, []byte("resolved-from-file"), 0o600))

	tests := []struct {
		name     string
		authType string
		options  any
	}{
		{
			name:     "bearer with file: token",
			authType: "bearer",
			options:  map[string]string{"token": "file:" + secretPath},
		},
		{
			name:     "api_key with file: value",
			authType: "api_key",
			options:  map[string]string{"key": "X-API", "value": "file:" + secretPath},
		},
		{
			name:     "oauth2 with file: client_secret",
			authType: "oauth2",
			options: map[string]any{
				"token_url":     "http://auth",
				"client_id":     "id",
				"client_secret": "file:" + secretPath,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optsJSON, err := json.Marshal(tt.options)
			require.NoError(t, err)

			authn, err := Build(tt.authType, optsJSON)
			require.NoError(t, err)
			assert.NotNil(t, authn)
		})
	}
}

func TestBuild_FailsOnUnresolvableFileRef(t *testing.T) {
	tests := []struct {
		name     string
		authType string
		options  any
	}{
		{
			name:     "bearer with missing file",
			authType: "bearer",
			options:  map[string]string{"token": "file:/no/such/file"},
		},
		{
			name:     "api_key with missing file",
			authType: "api_key",
			options:  map[string]string{"key": "X-API", "value": "file:/no/such/file"},
		},
		{
			name:     "oauth2 with missing file",
			authType: "oauth2",
			options: map[string]any{
				"token_url":     "http://auth",
				"client_id":     "id",
				"client_secret": "file:/no/such/file",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optsJSON, err := json.Marshal(tt.options)
			require.NoError(t, err)

			_, err = Build(tt.authType, optsJSON)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "failed to read secret file")
		})
	}
}
