// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import "testing"

func TestForcesDownload(t *testing.T) {
	tests := []struct {
		contentType string
		want        bool
	}{
		{"text/xml", true},
		{"application/xml", true},
		{"TEXT/XML", true},
		{"text/xml; charset=utf-8", true},
		{"application/xml;charset=UTF-8", true},
		{"text/xml; charset=", true},
		{"application/xhtml+xml", true},
		{"image/svg+xml", true},
		{"application/atom+xml", true},

		{"application/pdf", false},
		{"image/png", false},
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", false},
		{"text/html", false},
		{"text/html; charset=utf-8", false},
		{"text/plain", false},
		{"application/json", false},
		{"application/xmlfoo", false},
		{"application/vnd.xml", false},
		{DefaultMime, false},
		{"", false},
		{"xml", false},
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			if got := ForcesDownload(tt.contentType); got != tt.want {
				t.Errorf("ForcesDownload(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}
