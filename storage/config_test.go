// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"testing"

	"github.com/OpenNSW/core/storage/drivers"
)

func TestConfigValidateOKLocal(t *testing.T) {
	cfg := Config{
		Type:              TypeLocal,
		Local:             drivers.LocalConfig{BaseDir: "/data", PublicURL: "https://files.example.com", PutSecret: "s3cr3t"},
		PresignTTLSeconds: 900,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateOKS3(t *testing.T) {
	cfg := Config{
		Type:              TypeS3,
		S3:                drivers.S3Config{Endpoint: "https://s3.amazonaws.com", Bucket: "uploads", Region: "us-east-1"},
		PresignTTLSeconds: 900,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateUnsupportedType(t *testing.T) {
	cfg := Config{Type: "gcs", PresignTTLSeconds: 900}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for unsupported Type")
	}
}

func TestConfigValidateMissingPresignTTL(t *testing.T) {
	cfg := Config{
		Type: TypeS3,
		S3:   drivers.S3Config{Endpoint: "https://s3.amazonaws.com", Bucket: "uploads", Region: "us-east-1"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing PresignTTLSeconds")
	}
}

func TestConfigValidatePropagatesLocalError(t *testing.T) {
	cfg := Config{Type: TypeLocal, PresignTTLSeconds: 900} // empty drivers.LocalConfig
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error from an invalid Local config")
	}
}

func TestConfigValidatePropagatesS3Error(t *testing.T) {
	cfg := Config{Type: TypeS3, PresignTTLSeconds: 900} // empty drivers.S3Config
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error from an invalid S3 config")
	}
}
