// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3Driver_PresignTTL(t *testing.T) {
	client := s3.New(s3.Options{Region: "us-east-1"})

	if got := NewS3Driver(client, "uploads", "", 5*time.Minute).PresignTTL(); got != 5*time.Minute {
		t.Errorf("PresignTTL() = %v, want %v", got, 5*time.Minute)
	}
	if got := NewS3Driver(client, "uploads", "", 0).PresignTTL(); got != DefaultPresignTTL {
		t.Errorf("PresignTTL() = %v, want %v", got, DefaultPresignTTL)
	}
}
