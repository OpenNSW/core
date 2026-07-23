// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package secret

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecret_Resolve_Literal(t *testing.T) {
	t.Run("plain value is a literal", func(t *testing.T) {
		v, err := SecretRef("plain-value").Resolve()
		require.NoError(t, err)
		assert.Equal(t, "plain-value", v)
	})

	t.Run("unknown scheme is a literal", func(t *testing.T) {
		// A URL-like value whose prefix is not a known scheme stays literal.
		v, err := SecretRef("https://idp.example.gov/token").Resolve()
		require.NoError(t, err)
		assert.Equal(t, "https://idp.example.gov/token", v)
	})

	t.Run("literal: escape hatch strips the prefix", func(t *testing.T) {
		v, err := SecretRef("literal:env:NOT_A_REFERENCE").Resolve()
		require.NoError(t, err)
		assert.Equal(t, "env:NOT_A_REFERENCE", v)
	})

	t.Run("empty literal is allowed", func(t *testing.T) {
		v, err := SecretRef("").Resolve()
		require.NoError(t, err)
		assert.Equal(t, "", v)
	})

	t.Run("zero value resolves to empty", func(t *testing.T) {
		var s SecretRef
		v, err := s.Resolve()
		require.NoError(t, err)
		assert.Equal(t, "", v)
	})
}

func TestSecret_Resolve_Env(t *testing.T) {
	t.Run("resolves a set variable", func(t *testing.T) {
		t.Setenv("NPQS_CLIENT_SECRET", "from-env")
		v, err := SecretRef("env:NPQS_CLIENT_SECRET").Resolve()
		require.NoError(t, err)
		assert.Equal(t, "from-env", v)
	})

	t.Run("fails loud when unset", func(t *testing.T) {
		_, err := SecretRef("env:DEFINITELY_UNSET_VAR").Resolve()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not set or is empty")
	})

	t.Run("fails loud when empty", func(t *testing.T) {
		t.Setenv("EMPTY_VAR", "")
		_, err := SecretRef("env:EMPTY_VAR").Resolve()
		require.Error(t, err)
	})
}

func TestSecret_Resolve_File(t *testing.T) {
	dir := t.TempDir()

	t.Run("resolves and trims file contents", func(t *testing.T) {
		path := filepath.Join(dir, "token")
		require.NoError(t, os.WriteFile(path, []byte("  secret-token\n"), 0o600))

		v, err := SecretRef("file:" + path).Resolve()
		require.NoError(t, err)
		assert.Equal(t, "secret-token", v)
	})

	t.Run("fails loud when missing", func(t *testing.T) {
		_, err := SecretRef("file:" + filepath.Join(dir, "nope")).Resolve()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read secret file")
	})

	t.Run("fails loud when empty", func(t *testing.T) {
		path := filepath.Join(dir, "empty")
		require.NoError(t, os.WriteFile(path, []byte("   \n"), 0o600))
		_, err := SecretRef("file:" + path).Resolve()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "file is empty")
	})

	t.Run("rejects a directory", func(t *testing.T) {
		_, err := SecretRef("file:" + dir).Resolve()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
	})

	t.Run("rejects an oversized file", func(t *testing.T) {
		path := filepath.Join(dir, "big")
		require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", maxSecretFileSize+1)), 0o600))
		_, err := SecretRef("file:" + path).Resolve()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum")
	})
}

func TestSecret_JSON(t *testing.T) {
	t.Run("unmarshals a string", func(t *testing.T) {
		var s SecretRef
		require.NoError(t, json.Unmarshal([]byte(`"env:FOO"`), &s))
		t.Setenv("FOO", "bar")
		v, err := s.Resolve()
		require.NoError(t, err)
		assert.Equal(t, "bar", v)
	})

	t.Run("rejects a non-string (object form is not supported)", func(t *testing.T) {
		var s SecretRef
		err := json.Unmarshal([]byte(`{"env":"FOO"}`), &s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot unmarshal object")
	})

	t.Run("marshals back to the reference, not the value", func(t *testing.T) {
		b, err := json.Marshal(SecretRef("env:FOO"))
		require.NoError(t, err)
		assert.JSONEq(t, `"env:FOO"`, string(b))
	})
}
