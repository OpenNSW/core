// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import (
	"testing"
)

func TestS3Driver_RewriteHost(t *testing.T) {
	tests := []struct {
		name      string
		publicURL string
		rawURL    string
		wantURL   string
		wantErr   bool
	}{
		{
			name:      "no public url",
			publicURL: "",
			rawURL:    "https://real-s3.com/bucket/file?sig=123",
			wantURL:   "https://real-s3.com/bucket/file?sig=123",
			wantErr:   false,
		},
		{
			name:      "with public url",
			publicURL: "https://proxy.com",
			rawURL:    "https://real-s3.com/bucket/file?sig=123",
			wantURL:   "https://proxy.com/bucket/file?sig=123",
			wantErr:   false,
		},
		{
			name:      "with public url and HTTP scheme",
			publicURL: "http://proxy-http.com",
			rawURL:    "https://real-s3.com/bucket/file?sig=123",
			wantURL:   "http://proxy-http.com/bucket/file?sig=123",
			wantErr:   false,
		},
		{
			name:      "with public url and path prefix",
			publicURL: "https://proxy.com/prefix",
			rawURL:    "https://real-s3.com/bucket/file?sig=123",
			wantURL:   "https://proxy.com/prefix/bucket/file?sig=123",
			wantErr:   false,
		},
		{
			name:      "with public url and path prefix and trailing slash",
			publicURL: "https://proxy.com/prefix/",
			rawURL:    "https://real-s3.com/bucket/file?sig=123",
			wantURL:   "https://proxy.com/prefix/bucket/file?sig=123",
			wantErr:   false,
		},
		{
			name:      "invalid raw URL",
			publicURL: "https://proxy.com",
			rawURL:    "://invalid-url",
			wantErr:   true,
		},
		{
			name:      "invalid public URL",
			publicURL: "://invalid-public",
			rawURL:    "https://real-s3.com/bucket/file",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &S3Driver{
				PublicURL: tt.publicURL,
			}
			got, err := d.rewriteHost(tt.rawURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("rewriteHost() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.wantURL {
				t.Errorf("rewriteHost() = %q, want %q", got, tt.wantURL)
			}
		})
	}
}
