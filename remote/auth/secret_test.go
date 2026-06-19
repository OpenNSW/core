// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretRef_Resolve_PlainLiteral(t *testing.T) {
	val, err := SecretRef("my-secret").Resolve()
	require.NoError(t, err)
	assert.Equal(t, "my-secret", val)
}

func TestSecretRef_Resolve_EmptyLiteral(t *testing.T) {
	val, err := SecretRef("").Resolve()
	require.NoError(t, err)
	assert.Equal(t, "", val)
}

func TestSecretRef_Resolve_LiteralEscape(t *testing.T) {
	// "literal:file:/etc/passwd" should resolve to the string "file:/etc/passwd"
	val, err := SecretRef("literal:file:/etc/passwd").Resolve()
	require.NoError(t, err)
	assert.Equal(t, "file:/etc/passwd", val)
}

func TestSecretRef_Resolve_UnknownScheme(t *testing.T) {
	// An unknown scheme prefix is treated as a plain literal.
	val, err := SecretRef("vault:secret/data/key").Resolve()
	require.NoError(t, err)
	assert.Equal(t, "vault:secret/data/key", val)
}

func TestSecretRef_Resolve_FileSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(path, []byte("  file-secret-value  \n"), 0o600))

	val, err := SecretRef("file:" + path).Resolve()
	require.NoError(t, err)
	assert.Equal(t, "file-secret-value", val)
}

func TestSecretRef_Resolve_FileMissing(t *testing.T) {
	_, err := SecretRef("file:/definitely/does/not/exist").Resolve()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read secret file")
}

func TestSecretRef_Resolve_FileEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(path, []byte("   \n"), 0o600))

	_, err := SecretRef("file:" + path).Resolve()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file is empty")
}

func TestSecretRef_Resolve_FileIsDirectory(t *testing.T) {
	_, err := SecretRef("file:" + t.TempDir()).Resolve()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestSecretRef_Resolve_FileOversized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", 4097)), 0o600))

	_, err := SecretRef("file:" + path).Resolve()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestSecretRef_JSONRoundTrip(t *testing.T) {
	// SecretRef is a plain string alias, so JSON marshal/unmarshal works via the
	// enclosing struct's json tags — no custom marshaler needed. This test just
	// confirms that config structs using SecretRef decode correctly.
	type cfg struct {
		Token SecretRef `json:"token"`
	}
	import_json := []byte(`{"token":"file:/run/secrets/tok"}`)
	var c cfg
	require.NoError(t, json.Unmarshal(import_json, &c))
	assert.Equal(t, SecretRef("file:/run/secrets/tok"), c.Token)
}
