// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"context"
	"testing"

	"github.com/OpenNSW/core/storage/drivers"
)

func TestNewStorageFromConfig_LocalRoutePrefix(t *testing.T) {
	tests := []struct {
		name        string
		prefix      string
		wantPattern string
	}{
		{name: "default", prefix: "", wantPattern: "/api/v1/storage/{key}/content"},
		{name: "configured", prefix: "/files", wantPattern: "/files/{key}/content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver, err := NewStorageFromConfig(context.Background(), Config{
				Type: TypeLocal,
				Local: drivers.LocalConfig{
					BaseDir:     t.TempDir(),
					PublicURL:   "http://localhost:8080",
					PutSecret:   "secret",
					RoutePrefix: tt.prefix,
				},
				PresignTTLSeconds: 900,
			})
			if err != nil {
				t.Fatalf("NewStorageFromConfig: %v", err)
			}
			local, ok := driver.(*drivers.LocalFSDriver)
			if !ok {
				t.Fatalf("expected *drivers.LocalFSDriver, got %T", driver)
			}
			if got := local.ContentPattern(); got != tt.wantPattern {
				t.Errorf("ContentPattern() = %q, want %q", got, tt.wantPattern)
			}
		})
	}
}
