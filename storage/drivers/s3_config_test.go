// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import "testing"

func TestS3ConfigValidateOK(t *testing.T) {
	cfg := S3Config{Endpoint: "https://s3.amazonaws.com", Bucket: "uploads", Region: "us-east-1"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestS3ConfigValidateOKWithCredsAndPublicURL(t *testing.T) {
	cfg := S3Config{
		Endpoint: "http://localhost:9000", Bucket: "uploads", Region: "us-east-1",
		AccessKey: "ak", SecretKey: "sk", PublicURL: "https://cdn.example.com",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestS3ConfigValidateMissingEndpoint(t *testing.T) {
	cfg := S3Config{Bucket: "uploads", Region: "us-east-1"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing Endpoint")
	}
}

func TestS3ConfigValidateMissingBucket(t *testing.T) {
	cfg := S3Config{Endpoint: "https://s3.amazonaws.com", Region: "us-east-1"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing Bucket")
	}
}

func TestS3ConfigValidateMissingRegion(t *testing.T) {
	cfg := S3Config{Endpoint: "https://s3.amazonaws.com", Bucket: "uploads"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing Region")
	}
}

func TestS3ConfigValidateCredentialsMustPair(t *testing.T) {
	cfg := S3Config{Endpoint: "https://s3.amazonaws.com", Bucket: "uploads", Region: "us-east-1", AccessKey: "ak"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for AccessKey without SecretKey")
	}
}

func TestS3ConfigValidateInvalidPublicURL(t *testing.T) {
	cfg := S3Config{Endpoint: "https://s3.amazonaws.com", Bucket: "uploads", Region: "us-east-1", PublicURL: "not-a-url"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for invalid PublicURL")
	}
}
