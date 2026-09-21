// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"testing"

	"gopkg.in/yaml.v3"

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

func TestConfigValidatePresignTTLSecondsAtMax(t *testing.T) {
	cfg := Config{
		Type:              TypeS3,
		S3:                drivers.S3Config{Bucket: "uploads", Region: "us-east-1"},
		PresignTTLSeconds: int(maxPresignTTLSeconds),
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil at the maximum representable value", err)
	}
}

func TestConfigValidatePresignTTLSecondsOverflow(t *testing.T) {
	cfg := Config{
		Type:              TypeS3,
		S3:                drivers.S3Config{Bucket: "uploads", Region: "us-east-1"},
		PresignTTLSeconds: int(maxPresignTTLSeconds) + 1,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for PresignTTLSeconds beyond the maximum representable duration")
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

// A config shaped exactly like the S3 example in README.md — no Endpoint —
// must decode and validate cleanly. Endpoint is optional (empty targets AWS
// S3); this guards against that README example drifting out of sync with
// Config.Validate() again.
func TestConfigYAMLDecodeS3WithoutEndpoint(t *testing.T) {
	data := []byte(`
type: s3
s3:
  bucket: my-uploads
  region: ap-southeast-2
  accessKey: AKIAEXAMPLE
  secretKey: secret-example
presignTTLSeconds: 900
`)
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.S3.Endpoint != "" {
		t.Errorf("S3.Endpoint = %q, want empty", cfg.S3.Endpoint)
	}
	if cfg.S3.Bucket != "my-uploads" {
		t.Errorf("S3.Bucket = %q, want %q", cfg.S3.Bucket, "my-uploads")
	}
	if cfg.PresignTTLSeconds != 900 {
		t.Errorf("PresignTTLSeconds = %d, want 900", cfg.PresignTTLSeconds)
	}
}
