// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
)

var (
	ErrProhibitedFileType = errors.New("prohibited file type or extension")
	ErrMimeMismatch       = errors.New("file content does not match declared MIME type")
	ErrHeaderTooShort     = errors.New("file content is too short for magic byte validation")
	ErrInvalidFilename    = errors.New("invalid or unsafe filename")
)

var allowedExtensions = map[string]struct{}{
	".pdf":  {},
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".gif":  {},
	".webp": {},
	".tiff": {},
	".tif":  {},
	".csv":  {},
	".txt":  {},
	".json": {},
	".doc":  {},
	".docx": {},
	".xls":  {},
	".xlsx": {},
}

var prohibitedMimeTypes = map[string]struct{}{
	"image/svg+xml":            {},
	"text/html":                {},
	"application/x-executable": {},
	"application/x-msdownload": {},
	"text/x-php":               {},
	"application/x-sh":         {},
	"application/javascript":   {},
	"text/javascript":          {},
	"application/xml":          {},
	"text/xml":                 {},
	"application/x-bat":        {},
}

// CleanFilename sanitizes the input filename and validates its extension against allowed types.
func CleanFilename(filename string) (string, error) {
	if strings.Contains(filename, "\x00") {
		return "", fmt.Errorf("%w: null byte detected", ErrInvalidFilename)
	}

	cleanName := filepath.Base(filepath.Clean(filename))
	if cleanName == "." || cleanName == "/" || cleanName == "\\" || cleanName == "" {
		return "", fmt.Errorf("%w: empty or invalid name", ErrInvalidFilename)
	}

	ext := strings.ToLower(filepath.Ext(cleanName))
	if ext == "" {
		return "", fmt.Errorf("%w: missing file extension", ErrInvalidFilename)
	}

	if _, allowed := allowedExtensions[ext]; !allowed {
		return "", fmt.Errorf("%w: extension %s is not permitted", ErrProhibitedFileType, ext)
	}

	return cleanName, nil
}

// CheckFilenameExtension checks if the extension is permitted.
func CheckFilenameExtension(filename string) error {
	_, err := CleanFilename(filename)
	return err
}

// ValidateHeader checks magic bytes and detected content type against declared MIME type.
func ValidateHeader(header []byte, declaredMime string) error {
	if len(header) == 0 {
		return ErrHeaderTooShort
	}

	// Clean declared MIME type (strip parameters like ; charset=utf-8)
	cleanDeclaredMime := strings.ToLower(strings.TrimSpace(strings.Split(declaredMime, ";")[0]))

	// Clean detected MIME type
	detectedRaw := http.DetectContentType(header)
	detectedMime := strings.ToLower(strings.TrimSpace(strings.Split(detectedRaw, ";")[0]))

	// 1. Check detected MIME type against prohibited list
	if _, prohibited := prohibitedMimeTypes[detectedMime]; prohibited {
		return fmt.Errorf("%w: detected content type %s is dangerous/prohibited", ErrProhibitedFileType, detectedMime)
	}

	// 2. Validate magic bytes match the declared MIME type
	switch cleanDeclaredMime {
	case "application/pdf":
		if !bytes.HasPrefix(header, []byte("%PDF-")) {
			return fmt.Errorf("%w: expected PDF header %%PDF-", ErrMimeMismatch)
		}
	case "image/png":
		if !bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")) {
			return fmt.Errorf("%w: expected PNG header", ErrMimeMismatch)
		}
	case "image/jpeg":
		if !bytes.HasPrefix(header, []byte{0xFF, 0xD8, 0xFF}) {
			return fmt.Errorf("%w: expected JPEG header", ErrMimeMismatch)
		}
	case "image/gif":
		if !bytes.HasPrefix(header, []byte("GIF87a")) && !bytes.HasPrefix(header, []byte("GIF89a")) {
			return fmt.Errorf("%w: expected GIF header", ErrMimeMismatch)
		}
	case "image/webp":
		if len(header) < 12 || !bytes.HasPrefix(header, []byte("RIFF")) || string(header[8:12]) != "WEBP" {
			return fmt.Errorf("%w: expected WEBP header", ErrMimeMismatch)
		}
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		if !bytes.HasPrefix(header, []byte{0x50, 0x4B, 0x03, 0x04}) {
			return fmt.Errorf("%w: expected OpenXML/ZIP header (PK)", ErrMimeMismatch)
		}
	case "application/msword":
		if !bytes.HasPrefix(header, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}) {
			return fmt.Errorf("%w: expected legacy Word binary header", ErrMimeMismatch)
		}
	case "image/tiff":
		if !bytes.HasPrefix(header, []byte{0x49, 0x49, 0x2A, 0x00}) && !bytes.HasPrefix(header, []byte{0x4D, 0x4D, 0x00, 0x2A}) {
			return fmt.Errorf("%w: expected TIFF header", ErrMimeMismatch)
		}
	case "text/plain", "text/csv", "application/json":
		lowerHeader := bytes.ToLower(header)
		if bytes.Contains(lowerHeader, []byte("<script")) || bytes.Contains(lowerHeader, []byte("<html")) || bytes.Contains(lowerHeader, []byte("<svg")) {
			return fmt.Errorf("%w: text file contains prohibited HTML or script markup", ErrProhibitedFileType)
		}
	default:
		return fmt.Errorf("%w: unsupported declared MIME type %s", ErrProhibitedFileType, declaredMime)
	}

	return nil
}
