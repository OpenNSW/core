// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"archive/zip"
	"bytes"
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

func createMockZip(files map[string]string) []byte {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	for name, content := range files {
		f, _ := w.Create(name)
		_, _ = f.Write([]byte(content))
	}
	_ = w.Close()
	return buf.Bytes()
}

func TestValidateHeader_ValidXLSX(t *testing.T) {
	xlsxZip := createMockZip(map[string]string{
		"[Content_Types].xml": "<Types></Types>",
		"xl/workbook.xml":     "<workbook></workbook>",
	})
	if err := ValidateHeader(xlsxZip, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"); err != nil {
		t.Fatalf("expected valid XLSX header, got error: %v", err)
	}
}

func TestValidateHeader_ValidDOCX(t *testing.T) {
	docxZip := createMockZip(map[string]string{
		"[Content_Types].xml": "<Types></Types>",
		"word/document.xml":   "<document></document>",
	})
	if err := ValidateHeader(docxZip, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"); err != nil {
		t.Fatalf("expected valid DOCX header, got error: %v", err)
	}
}

func TestValidateHeader_GenericZipAsXLSX_Fails(t *testing.T) {
	genericZip := createMockZip(map[string]string{
		"file.txt": "hello world",
	})
	if err := ValidateHeader(genericZip, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"); err == nil {
		t.Fatal("expected generic ZIP declared as XLSX to be rejected, got nil")
	}
}

func TestValidateHeader_ValidDOC(t *testing.T) {
	docHeader := []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	if err := ValidateHeader(docHeader, "application/msword"); err != nil {
		t.Fatalf("expected valid DOC header, got error: %v", err)
	}
}

func TestValidateHeader_ValidTIFF(t *testing.T) {
	tiffHeader := []byte{0x49, 0x49, 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00}
	if err := ValidateHeader(tiffHeader, "image/tiff"); err != nil {
		t.Fatalf("expected valid TIFF header, got error: %v", err)
	}
}

func TestValidateHeader_ValidCSV(t *testing.T) {
	csvData := []byte("Header1,Header2\nValue1,Value2")
	if err := ValidateHeader(csvData, "text/csv"); err != nil {
		t.Fatalf("expected valid CSV data, got error: %v", err)
	}
}

func TestValidateHeader_TextWithScriptTag_Fails(t *testing.T) {
	maliciousText := []byte("col1,col2\n<script>alert('xss')</script>")
	if err := ValidateHeader(maliciousText, "text/csv"); err == nil {
		t.Fatal("expected script tag in CSV to be rejected, got nil")
	}
}

func TestValidateTextContent_ExpandedMarkers(t *testing.T) {
	vectors := [][]byte{
		[]byte("<iframe src='evil.com'></iframe>"),
		[]byte("<object data='evil.swf'></object>"),
		[]byte("<embed src='evil.swf'>"),
		[]byte("<meta http-equiv='refresh' content='0;url=http://evil.com'>"),
		[]byte("<a href='javascript:alert(1)'>click</a>"),
		[]byte("<a href='vbscript:msgbox(1)'>click</a>"),
		[]byte("<iframe src='data:text/html,<script>alert(1)</script>'>"),
	}

	for _, v := range vectors {
		if err := ValidateTextContent(v); err == nil {
			t.Errorf("expected vector %q to be rejected, got nil", string(v))
		}
	}
}

func TestValidateTextContent_InlineEventHandlers(t *testing.T) {
	vectors := [][]byte{
		[]byte("<img src=x onerror=alert(1)>"),
		[]byte("<body onload=alert(1)>"),
		[]byte("<svg/onload=alert(1)>"),
		[]byte("<div onclick = alert(1)>"),
	}

	for _, v := range vectors {
		if err := ValidateTextContent(v); err == nil {
			t.Errorf("expected inline event handler vector %q to be rejected, got nil", string(v))
		}
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

func TestValidateHeader_BinaryPayloadLabeledAsJSON(t *testing.T) {
	elfHeader := []byte("\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	if err := ValidateHeader(elfHeader, "application/json"); err == nil {
		t.Fatal("expected binary ELF payload declared as application/json to be rejected, got nil")
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
		{"script.sh", "", true},
		{"shell.phtml", "", true},
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
	prohibited := []string{"malware.exe", "script.sh", "page.html", "vector.svg", "shell.phtml", "payload.phar"}
	for _, fname := range prohibited {
		if err := CheckFilenameExtension(fname); err == nil {
			t.Errorf("expected prohibited extension error for %s, got nil", fname)
		}
	}
}
