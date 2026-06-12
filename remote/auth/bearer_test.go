// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBearer_Apply(t *testing.T) {
	t.Run("literal token", func(t *testing.T) {
		auth := NewBearer(BearerConfig{Token: NewSecret("my-token")})
		req, _ := http.NewRequest(http.MethodGet, "http://local", nil)

		err := auth.Apply(req)
		assert.NoError(t, err)
		assert.Equal(t, "Bearer my-token", req.Header.Get("Authorization"))
	})

	t.Run("env resolved token", func(t *testing.T) {
		t.Setenv("BEARER_TOKEN_ENV", "env-secret-token")
		auth := NewBearer(BearerConfig{Token: NewSecret("env:BEARER_TOKEN_ENV")})
		req, _ := http.NewRequest(http.MethodGet, "http://local", nil)

		err := auth.Apply(req)
		assert.NoError(t, err)
		assert.Equal(t, "Bearer env-secret-token", req.Header.Get("Authorization"))
	})

	t.Run("resolution error", func(t *testing.T) {
		auth := NewBearer(BearerConfig{Token: NewSecret("env:BEARER_TOKEN_ENV_MISSING_123")})
		req, _ := http.NewRequest(http.MethodGet, "http://local", nil)

		err := auth.Apply(req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "bearer token resolution failed")
	})
}
