// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package configyaml

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadAndExpand_ResolvesEnvPlaceholder(t *testing.T) {
	t.Setenv("CONFIGYAML_TEST_SECRET", "s3cr3t")
	path := writeFile(t, "password: \"{{env:CONFIGYAML_TEST_SECRET}}\"\n")

	var out struct {
		Password string `yaml:"password"`
	}
	require.NoError(t, LoadAndExpand(path, &out))
	assert.Equal(t, "s3cr3t", out.Password)
}

func TestLoadAndExpand_ResolvesFilePlaceholder(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(secretFile, []byte("from-a-file\n"), 0o600))
	path := writeFile(t, "token: \"{{file:"+secretFile+"}}\"\n")

	var out struct {
		Token string `yaml:"token"`
	}
	require.NoError(t, LoadAndExpand(path, &out))
	assert.Equal(t, "from-a-file", out.Token)
}

// Placeholder resolution isn't limited to string fields — a non-string (int)
// field templated the same way must still decode to the right type, proving
// the resolved node's tag/style reset works.
func TestLoadAndExpand_ResolvesPlaceholderOnNonStringField(t *testing.T) {
	t.Setenv("CONFIGYAML_TEST_PORT", "9999")
	path := writeFile(t, "port: \"{{env:CONFIGYAML_TEST_PORT}}\"\n")

	var out struct {
		Port int `yaml:"port"`
	}
	require.NoError(t, LoadAndExpand(path, &out))
	assert.Equal(t, 9999, out.Port)
}

// A value that only partly looks like a placeholder (extra text alongside
// the braces) is left as a literal, not resolved — the whole scalar must be
// the placeholder.
func TestLoadAndExpand_PartialPlaceholderIsLiteral(t *testing.T) {
	path := writeFile(t, "value: \"prefix-{{env:SOME_VAR}}-suffix\"\n")

	var out struct {
		Value string `yaml:"value"`
	}
	require.NoError(t, LoadAndExpand(path, &out))
	assert.Equal(t, "prefix-{{env:SOME_VAR}}-suffix", out.Value)
}

func TestLoadAndExpand_UnsetEnvFailsClosed(t *testing.T) {
	path := writeFile(t, "nested:\n  password: \"{{env:CONFIGYAML_TEST_DOES_NOT_EXIST}}\"\n")

	var out struct {
		Nested struct {
			Password string `yaml:"password"`
		} `yaml:"nested"`
	}
	err := LoadAndExpand(path, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nested.password")
}

func TestLoadAndExpand_MissingFile(t *testing.T) {
	err := LoadAndExpand(filepath.Join(t.TempDir(), "does-not-exist.yaml"), &struct{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading config file")
}

func TestLoadAndExpand_MalformedYAML(t *testing.T) {
	path := writeFile(t, "not: valid: yaml: [")

	err := LoadAndExpand(path, &struct{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

// A struct embedding a config type with real yaml tags (as database.Config,
// temporal.Config, and cors.Config now carry) decodes exactly like any other
// struct — that's the point of this package pairing with those tags.
func TestLoadAndExpand_EmbeddedTaggedStruct(t *testing.T) {
	t.Setenv("CONFIGYAML_TEST_DB_PASSWORD", "hunter2")
	path := writeFile(t, "db:\n  host: localhost\n  port: 5432\n  password: \"{{env:CONFIGYAML_TEST_DB_PASSWORD}}\"\n")

	var out struct {
		DB struct {
			Host     string `yaml:"host"`
			Port     int    `yaml:"port"`
			Password string `yaml:"password"`
		} `yaml:"db"`
	}
	require.NoError(t, LoadAndExpand(path, &out))
	assert.Equal(t, "localhost", out.DB.Host)
	assert.Equal(t, 5432, out.DB.Port)
	assert.Equal(t, "hunter2", out.DB.Password)
}

// A placeholder reached only through a YAML alias (*ref) must resolve the
// same as one reached directly, regardless of whether the anchor or the
// alias appears first in the document.
func TestLoadAndExpand_ResolvesAliasedPlaceholder(t *testing.T) {
	t.Setenv("CONFIGYAML_TEST_ALIAS_SECRET", "shared-secret")
	path := writeFile(t, "base: &creds \"{{env:CONFIGYAML_TEST_ALIAS_SECRET}}\"\nalias: *creds\n")

	var out struct {
		Base  string `yaml:"base"`
		Alias string `yaml:"alias"`
	}
	require.NoError(t, LoadAndExpand(path, &out))
	assert.Equal(t, "shared-secret", out.Base)
	assert.Equal(t, "shared-secret", out.Alias)
}
