// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package loaders

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/loaders/github"
	"github.com/OpenNSW/core/artifact/loaders/local"
	"github.com/OpenNSW/core/artifact/loaders/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Validate ---

func TestConfig_Validate(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name    string
		cfg     Config
		wantErr string // substring; "" means no error
	}{
		{
			name: "local ok",
			cfg:  Config{Type: TypeLocal, Local: local.Config{Root: root}},
		},
		{
			name: "local trims type",
			cfg:  Config{Type: "  local  ", Local: local.Config{Root: root}},
		},
		{
			name:    "local missing root",
			cfg:     Config{Type: TypeLocal},
			wantErr: "Root is required",
		},
		{
			name: "github ok",
			cfg:  Config{Type: TypeGitHub, GitHub: github.Config{Owner: "o", Repo: "r", Ref: "main"}},
		},
		{
			name:    "github missing required fields",
			cfg:     Config{Type: TypeGitHub},
			wantErr: "github",
		},
		{
			name: "s3 ok",
			cfg:  Config{Type: TypeS3, S3: s3.Config{Bucket: "b", Region: "us-east-1"}},
		},
		{
			name:    "s3 missing required fields",
			cfg:     Config{Type: TypeS3},
			wantErr: "s3 loader",
		},
		{
			name:    "empty type",
			cfg:     Config{},
			wantErr: "Type is required",
		},
		{
			name:    "unsupported type",
			cfg:     Config{Type: "gcs"},
			wantErr: `unsupported type "gcs"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// --- New: local ---

func TestNew_Local(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "hello.txt"), []byte("world"), 0o600))

	loader, err := New(context.Background(), Config{
		Type:  TypeLocal,
		Local: local.Config{Root: root},
	})
	require.NoError(t, err)
	require.NotNil(t, loader)

	// The returned loader resolves paths against Root.
	data, err := loader.Load(context.Background(), "hello.txt")
	require.NoError(t, err)
	assert.Equal(t, "world", string(data))
}

func TestNew_Local_MissingFileIsNotFound(t *testing.T) {
	loader, err := New(context.Background(), Config{
		Type:  TypeLocal,
		Local: local.Config{Root: t.TempDir()},
	})
	require.NoError(t, err)

	_, err = loader.Load(context.Background(), "absent.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, artifact.ErrNotFound)
}

func TestNew_Local_InvalidConfig(t *testing.T) {
	// Root does not exist → New's Validate step catches it before construction.
	_, err := New(context.Background(), Config{
		Type:  TypeLocal,
		Local: local.Config{Root: filepath.Join(t.TempDir(), "does-not-exist")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local loader")
}

// --- New: github ---

func TestNew_GitHub(t *testing.T) {
	// github.New validates config but performs no network I/O at construction,
	// so a well-formed config yields a usable loader.
	loader, err := New(context.Background(), Config{
		Type: TypeGitHub,
		GitHub: github.Config{
			Owner: "OpenNSW",
			Repo:  "configs",
			Ref:   "main",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, loader)
}

func TestNew_GitHub_InvalidConfig(t *testing.T) {
	_, err := New(context.Background(), Config{Type: TypeGitHub}) // missing Owner/Repo/Ref
	require.Error(t, err)
	assert.Contains(t, err.Error(), "github loader")
}

// --- New: s3 ---

func TestNew_S3(t *testing.T) {
	// s3.New validates config and builds an AWS client but performs no network
	// I/O at construction (static credentials skip the credential chain), so a
	// well-formed config yields a usable loader without reaching a bucket.
	loader, err := New(context.Background(), Config{
		Type: TypeS3,
		S3: s3.Config{
			Bucket:    "my-artifacts",
			Region:    "us-east-1",
			AccessKey: "test-access",
			SecretKey: "test-secret",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, loader)
}

func TestNew_S3_InvalidConfig(t *testing.T) {
	_, err := New(context.Background(), Config{Type: TypeS3}) // missing Bucket/Region
	require.Error(t, err)
	assert.Contains(t, err.Error(), "s3 loader")
}

// --- New: type selection ---

func TestNew_UnsupportedType(t *testing.T) {
	_, err := New(context.Background(), Config{Type: "gcs"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported type "gcs"`)
}

func TestNew_EmptyType(t *testing.T) {
	_, err := New(context.Background(), Config{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Type is required")
}
