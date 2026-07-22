// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"testing"
)

func TestValidateHeader_ValidPDF(t *testing.T) {
	pdfHeader := []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	if err := ValidateHeader(pdfHeader, "application/pdf"); err != nil {
		t.Fatalf("expected valid PDF header, got error: %v", err)
	}
}

func TestValidateHeader_ValidPNG(t *testing.T) {
	pngHeader := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if err := ValidateHeader(pngHeader, "image/png"); err != nil {
		t.Fatalf("expected valid PNG header, got error: %v", err)
	}
}

func TestValidateHeader_ValidJPEG(t *testing.T) {
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46}
	if err := ValidateHeader(jpegHeader, "image/jpeg"); err != nil {
		t.Fatalf("expected valid JPEG header, got error: %v", err)
	}
}

func TestValidateHeader_ValidXLSX(t *testing.T) {
	xlsxHeader := []byte{0x50, 0x4B, 0x03, 0x04, 0x14, 0x00, 0x06, 0x00}
	if err := ValidateHeader(xlsxHeader, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"); err != nil {
		t.Fatalf("expected valid XLSX header, got error: %v", err)
	}
}

func TestValidateHeader_SpoofedPDF_WithHTMLContent(t *testing.T) {
	htmlHeader := []byte("<html><body><script>alert(1)</script></body></html>")
	err := ValidateHeader(htmlHeader, "application/pdf")
	if err == nil {
		t.Fatal("expected error for HTML content disguised as PDF, got nil")
	}
}

func TestValidateHeader_ProhibitedSVG(t *testing.T) {
	svgHeader := []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"><script>alert(1)</script></svg>")
	err := ValidateHeader(svgHeader, "image/svg+xml")
	if err == nil {
		t.Fatal("expected error for SVG image, got nil")
	}
}

func TestValidateHeader_ValidPDF_WithMimeParameter(t *testing.T) {
	pdfHeader := []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	if err := ValidateHeader(pdfHeader, "application/pdf; charset=utf-8"); err != nil {
		t.Fatalf("expected valid PDF header with mime parameter, got error: %v", err)
	}
}

func TestCleanFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		err      bool
	}{
		{"document.pdf", "document.pdf", false},
		{"../../etc/passwd.pdf", "passwd.pdf", false},
		{"malware.exe", "", true},
		{"file\x00name.pdf", "", true},
	}

	for _, tt := range tests {
		res, err := CleanFilename(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("CleanFilename(%q) error = %v, expect error %v", tt.input, err, tt.err)
		}
		if !tt.err && res != tt.expected {
			t.Errorf("CleanFilename(%q) = %q, expected %q", tt.input, res, tt.expected)
		}
	}
}

func TestCheckFilenameExtension_Prohibited(t *testing.T) {
	prohibited := []string{"malware.exe", "script.sh", "page.html", "vector.svg", "macro.xls"}
	for _, fname := range prohibited {
		if err := CheckFilenameExtension(fname); err == nil {
			t.Errorf("expected prohibited extension error for %s, got nil", fname)
		}
	}
}
