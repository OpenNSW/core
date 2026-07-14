// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package local

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigValidateOK(t *testing.T) {
	cfg := Config{Root: t.TempDir()}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateEmptyRoot(t *testing.T) {
	cfg := Config{}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for empty Root")
	}
}

func TestConfigValidateMissingRoot(t *testing.T) {
	cfg := Config{Root: filepath.Join(t.TempDir(), "does-not-exist")}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing Root")
	}
}

func TestConfigValidateRootIsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg := Config{Root: file}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for non-directory Root")
	}
}
