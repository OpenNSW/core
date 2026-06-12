// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPIKey_Apply(t *testing.T) {
	t.Run("literal value", func(t *testing.T) {
		auth := NewAPIKey(APIKeyConfig{Key: "X-Key", Value: NewSecret("secret")})
		req, _ := http.NewRequest(http.MethodGet, "http://local", nil)

		err := auth.Apply(req)
		assert.NoError(t, err)
		assert.Equal(t, "secret", req.Header.Get("X-Key"))
	})

	t.Run("env resolved value", func(t *testing.T) {
		t.Setenv("API_KEY_ENV", "env-api-key")
		auth := NewAPIKey(APIKeyConfig{Key: "X-Key", Value: NewSecret("env:API_KEY_ENV")})
		req, _ := http.NewRequest(http.MethodGet, "http://local", nil)

		err := auth.Apply(req)
		assert.NoError(t, err)
		assert.Equal(t, "env-api-key", req.Header.Get("X-Key"))
	})

	t.Run("resolution error", func(t *testing.T) {
		auth := NewAPIKey(APIKeyConfig{Key: "X-Key", Value: NewSecret("env:API_KEY_ENV_MISSING_123")})
		req, _ := http.NewRequest(http.MethodGet, "http://local", nil)

		err := auth.Apply(req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "apikey resolution failed")
	})
}
