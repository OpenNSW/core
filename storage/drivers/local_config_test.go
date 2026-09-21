// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import "testing"

func TestLocalConfigValidateOK(t *testing.T) {
	cfg := LocalConfig{BaseDir: "/data", PublicURL: "https://files.example.com", PutSecret: "s3cr3t"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLocalConfigValidateMissingBaseDir(t *testing.T) {
	cfg := LocalConfig{PublicURL: "https://files.example.com", PutSecret: "s3cr3t"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing BaseDir")
	}
}

func TestLocalConfigValidateMissingPublicURL(t *testing.T) {
	cfg := LocalConfig{BaseDir: "/data", PutSecret: "s3cr3t"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing PublicURL")
	}
}

func TestLocalConfigValidateInvalidPublicURL(t *testing.T) {
	cfg := LocalConfig{BaseDir: "/data", PublicURL: "not-a-url", PutSecret: "s3cr3t"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for invalid PublicURL")
	}
}

func TestLocalConfigValidateMissingPutSecret(t *testing.T) {
	cfg := LocalConfig{BaseDir: "/data", PublicURL: "https://files.example.com"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing PutSecret")
	}
}
