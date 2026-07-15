// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package s3

import "testing"

func TestConfigValidateOK(t *testing.T) {
	cfg := Config{Bucket: "artifacts", Region: "us-east-1"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateOKWithEndpointAndCreds(t *testing.T) {
	cfg := Config{
		Bucket: "artifacts", Region: "us-east-1",
		Endpoint: "http://localhost:9000", AccessKey: "ak", SecretKey: "sk",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateMissingBucket(t *testing.T) {
	cfg := Config{Region: "us-east-1"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing Bucket")
	}
}

func TestConfigValidateMissingRegion(t *testing.T) {
	cfg := Config{Bucket: "artifacts"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing Region")
	}
}

func TestConfigValidateCredentialsMustPair(t *testing.T) {
	cfg := Config{Bucket: "artifacts", Region: "us-east-1", AccessKey: "ak"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for AccessKey without SecretKey")
	}
}

func TestConfigValidateInvalidEndpoint(t *testing.T) {
	cfg := Config{Bucket: "artifacts", Region: "us-east-1", Endpoint: "not-a-url"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for invalid Endpoint")
	}
}
