// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSecret_UnmarshalJSON(t *testing.T) {
	t.Run("plain string literal", func(t *testing.T) {
		var s Secret
		err := json.Unmarshal([]byte(`"my-secret"`), &s)
		assert.NoError(t, err)
		assert.Equal(t, "literal", s.source)
		assert.Equal(t, "my-secret", s.value)
	})

	t.Run("prefixed env string", func(t *testing.T) {
		var s Secret
		err := json.Unmarshal([]byte(`"env:MY_VAR"`), &s)
		assert.NoError(t, err)
		assert.Equal(t, "env", s.source)
		assert.Equal(t, "MY_VAR", s.value)
	})

	t.Run("prefixed file string", func(t *testing.T) {
		var s Secret
		err := json.Unmarshal([]byte(`"file:/path/to/token"`), &s)
		assert.NoError(t, err)
		assert.Equal(t, "file", s.source)
		assert.Equal(t, "/path/to/token", s.value)
	})

	t.Run("env object", func(t *testing.T) {
		var s Secret
		err := json.Unmarshal([]byte(`{"env": "MY_VAR"}`), &s)
		assert.NoError(t, err)
		assert.Equal(t, "env", s.source)
		assert.Equal(t, "MY_VAR", s.value)
	})

	t.Run("file object", func(t *testing.T) {
		var s Secret
		err := json.Unmarshal([]byte(`{"file": "/path/to/token"}`), &s)
		assert.NoError(t, err)
		assert.Equal(t, "file", s.source)
		assert.Equal(t, "/path/to/token", s.value)
	})

	t.Run("literal object", func(t *testing.T) {
		var s Secret
		err := json.Unmarshal([]byte(`{"literal": "plain-text-literal"}`), &s)
		assert.NoError(t, err)
		assert.Equal(t, "literal", s.source)
		assert.Equal(t, "plain-text-literal", s.value)
	})

	t.Run("invalid object key", func(t *testing.T) {
		var s Secret
		err := json.Unmarshal([]byte(`{"invalid": "value"}`), &s)
		assert.Error(t, err)
	})
}

func TestSecret_Resolve(t *testing.T) {
	ctx := context.Background()

	t.Run("env resolver", func(t *testing.T) {
		t.Setenv("TEST_RESOLVER_ENV", "my-env-val")
		s := NewSecret("env:TEST_RESOLVER_ENV")
		val, err := s.Resolve(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "my-env-val", val)
	})

	t.Run("file resolver and rotation", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "rotated-secret")

		err := os.WriteFile(filePath, []byte("first-value"), 0600)
		assert.NoError(t, err)

		s := NewSecret("file:" + filePath)

		// First read
		val1, err := s.Resolve(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "first-value", val1)

		// Write new value
		err = os.WriteFile(filePath, []byte("rotated-value"), 0600)
		assert.NoError(t, err)

		// Immediate read should see rotated value (no caching)
		val2, err := s.Resolve(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "rotated-value", val2)
	})
}

func TestSecret_Validate(t *testing.T) {
	t.Run("valid env", func(t *testing.T) {
		t.Setenv("VALID_ENV", "exists")
		s := NewSecret("env:VALID_ENV")
		assert.NoError(t, s.Validate())
	})

	t.Run("invalid env", func(t *testing.T) {
		os.Unsetenv("INVALID_ENV")
		s := NewSecret("env:INVALID_ENV")
		assert.Error(t, s.Validate())
	})

	t.Run("valid file", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "valid-file")
		err := os.WriteFile(filePath, []byte("content"), 0600)
		assert.NoError(t, err)

		s := NewSecret("file:" + filePath)
		assert.NoError(t, s.Validate())
	})

	t.Run("missing file", func(t *testing.T) {
		s := NewSecret("file:/non-existent-secret-path")
		assert.Error(t, s.Validate())
	})

	t.Run("empty file", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "empty-file")
		err := os.WriteFile(filePath, []byte("  \n "), 0600)
		assert.NoError(t, err)

		s := NewSecret("file:" + filePath)
		assert.Error(t, s.Validate())
	})

	t.Run("oversized file rejected", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "big-file")
		// Write a file larger than maxSecretFileSize (4KB)
		bigContent := make([]byte, maxSecretFileSize+1)
		for i := range bigContent {
			bigContent[i] = 'x'
		}
		err := os.WriteFile(filePath, bigContent, 0600)
		assert.NoError(t, err)

		s := NewSecret("file:" + filePath)
		err = s.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum allowed size")
	})

	t.Run("directory rejected as non-regular file", func(t *testing.T) {
		tmpDir := t.TempDir()
		s := NewSecret("file:" + tmpDir)
		err := s.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
	})

	t.Run("valid literal", func(t *testing.T) {
		s := NewSecret("my-secret-value")
		assert.NoError(t, s.Validate())
	})

	t.Run("empty literal rejected", func(t *testing.T) {
		s := Secret{source: "literal", value: ""}
		assert.Error(t, s.Validate())
		assert.Contains(t, s.Validate().Error(), "literal secret value is empty")
	})
}
