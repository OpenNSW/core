// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"slices"
	"strings"
	"testing"

	"github.com/OpenNSW/core/storage/drivers"
)

func TestInlinePolicy_Default(t *testing.T) {
	tests := []struct {
		contentType string
		want        string
	}{
		{"application/pdf", drivers.DispositionInline},
		{"APPLICATION/PDF", drivers.DispositionInline},
		{"application/pdf; version=1.7", drivers.DispositionInline},
		{"image/png", drivers.DispositionInline},
		{"image/jpeg", drivers.DispositionInline},
		{"image/gif", drivers.DispositionInline},
		{"image/webp", drivers.DispositionInline},
		{"text/plain", drivers.DispositionInline},
		{"text/plain; charset=utf-8", drivers.DispositionInline},
		{"text/plain; charset=", drivers.DispositionInline},

		{"text/xml", drivers.DispositionAttachment},
		{"application/xml", drivers.DispositionAttachment},
		{"application/xhtml+xml", drivers.DispositionAttachment},
		{"image/svg+xml", drivers.DispositionAttachment},
		{"text/html", drivers.DispositionAttachment},
		{"text/html; charset=utf-8", drivers.DispositionAttachment},
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", drivers.DispositionAttachment},
		{"image/tiff", drivers.DispositionAttachment},
		{"image/*", drivers.DispositionAttachment},
		{"text/javascript", drivers.DispositionAttachment},
		{drivers.DefaultMime, drivers.DispositionAttachment},
		{"", drivers.DispositionAttachment},
		{"pdf", drivers.DispositionAttachment},
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			if got := defaultInline.disposition(tt.contentType); got != tt.want {
				t.Errorf("disposition(%q) = %q, want %q", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestNewInlinePolicy(t *testing.T) {
	policy := newInlinePolicy([]string{" TEXT/HTML ", "application/pdf; q=1", ""})

	tests := []struct {
		contentType string
		want        string
	}{
		{"text/html", drivers.DispositionInline},
		{"text/html; charset=utf-8", drivers.DispositionInline},
		{"application/pdf", drivers.DispositionInline},
		{"image/png", drivers.DispositionAttachment},
		{"text/plain", drivers.DispositionAttachment},
		{"", drivers.DispositionAttachment},
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			if got := policy.disposition(tt.contentType); got != tt.want {
				t.Errorf("disposition(%q) = %q, want %q", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestInlinePolicy_NoTypesSavesEverything(t *testing.T) {
	policy := newInlinePolicy(nil)
	for _, contentType := range DefaultInlineTypes() {
		if got := policy.disposition(contentType); got != drivers.DispositionAttachment {
			t.Errorf("disposition(%q) = %q, want %q", contentType, got, drivers.DispositionAttachment)
		}
	}
}

func TestDefaultInlineTypes_ReturnsACopy(t *testing.T) {
	first := DefaultInlineTypes()
	first[0] = "text/html"

	if got := DefaultInlineTypes(); slices.Contains(got, "text/html") {
		t.Errorf("DefaultInlineTypes() = %v, changed by a caller", got)
	}
	if got := defaultInline.disposition("text/html"); got != drivers.DispositionAttachment {
		t.Errorf("the default policy serves text/html as %q after a caller changed a returned list", got)
	}
}

func TestDefaultInlineTypes_AreValid(t *testing.T) {
	for _, contentType := range DefaultInlineTypes() {
		if err := ValidMediaType(contentType); err != nil {
			t.Errorf("default inline type %q: %v", contentType, err)
		}
	}
}

func TestValidMediaType(t *testing.T) {
	tests := []struct {
		mediaType string
		wantErr   bool
	}{
		{"application/pdf", false},
		{"text/html", false},
		{"image/svg+xml", false},
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", false},
		{"", true},
		{"pdf", true},
		{"Application/PDF", true},
		{"text/html; charset=utf-8", true},
		{"text/html;", true},
		{"image/*", true},
		{"*/*", true},
		{" text/html", true},
	}
	for _, tt := range tests {
		t.Run(tt.mediaType, func(t *testing.T) {
			if err := ValidMediaType(tt.mediaType); (err != nil) != tt.wantErr {
				t.Errorf("ValidMediaType(%q) error = %v, wantErr %v", tt.mediaType, err, tt.wantErr)
			}
		})
	}
}

func TestCheckUploadTypes(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		inline  []string
		wantErr []string // types the error must name; none means no error
	}{
		{name: "ordinary uploads, default inline types", allowed: []string{"application/pdf", "image/png", "application/xml"}, inline: DefaultInlineTypes()},
		{name: "no uploads", inline: []string{"text/html"}},
		{name: "html inline, html not uploadable", allowed: []string{"application/pdf", "image/png"}, inline: []string{"text/html"}},
		{name: "inline list holds only default types", allowed: []string{"application/pdf"}, inline: []string{"application/pdf"}},
		{name: "html both inline and uploadable", allowed: []string{"application/pdf", "text/html"}, inline: []string{"text/html"}, wantErr: []string{"text/html"}},
		{name: "parameters and case do not hide it", allowed: []string{"TEXT/HTML; charset=utf-8"}, inline: []string{"text/html"}, wantErr: []string{"TEXT/HTML; charset=utf-8"}},
		{name: "every offender is named once", allowed: []string{"text/html", "image/svg+xml", "text/html", "image/png"}, inline: []string{"text/html", "image/svg+xml"}, wantErr: []string{"text/html", "image/svg+xml"}},
		{name: "empty inline list opens nothing", allowed: []string{"text/html", "application/xml"}, inline: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckUploadTypes(tt.allowed, tt.inline)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("CheckUploadTypes() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckUploadTypes() = nil, want an error naming %v", tt.wantErr)
			}
			for _, name := range tt.wantErr {
				if got := strings.Count(err.Error(), name); got != 1 {
					t.Errorf("error %q names %q %d times, want once", err, name, got)
				}
			}
		})
	}
}
