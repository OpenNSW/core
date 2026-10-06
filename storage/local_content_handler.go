// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/OpenNSW/core/storage/drivers"
)

// LocalContentHandler serves the routes the local driver's upload and download
// URLs point at, standing in for S3 in local development. It is separate from
// HTTPHandler because it only exists for the local driver, and the app builds
// it only when that is the driver it chose.
//
// The routes take no other auth: like an S3 presigned URL, each request must
// carry the token the driver signed for that key and operation.
type LocalContentHandler struct {
	driver *drivers.LocalFSDriver
}

func NewLocalContentHandler(driver *drivers.LocalFSDriver) *LocalContentHandler {
	return &LocalContentHandler{driver: driver}
}

// RegisterRoutes serves PUT and GET on the driver's ContentPattern, the path
// the driver builds its URLs from.
func (h *LocalContentHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT "+h.driver.ContentPattern(), h.upload)
	mux.HandleFunc("GET "+h.driver.ContentPattern(), h.download)
}

// upload accepts the raw file body for an upload URL issued by
// GetUploadURL.
func (h *LocalContentHandler) upload(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "key is required")
		return
	}
	if !validStorageKey(key) {
		writeJSONError(w, http.StatusBadRequest, "invalid key format")
		return
	}

	// Extract security constraints from query parameters
	token := r.URL.Query().Get("token")
	expiresAtStr := r.URL.Query().Get("expiresAt")
	encodedContentType := r.URL.Query().Get("contentType")
	maxSizeBytesStr := r.URL.Query().Get("maxSizeBytes")

	if token == "" || expiresAtStr == "" || encodedContentType == "" || maxSizeBytesStr == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing security token or constraints")
		return
	}

	expiresAt, err := strconv.ParseInt(expiresAtStr, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid expiration format")
		return
	}

	maxSizeBytes, err := strconv.ParseInt(maxSizeBytesStr, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid max size format")
		return
	}

	// Verify HMAC token (signs all constraints)
	if !h.driver.VerifyToken(key, token, expiresAt, encodedContentType, maxSizeBytes) {
		writeJSONError(w, http.StatusUnauthorized, "invalid security token")
		return
	}

	// 1. Enforce TTL (Time-To-Live)
	if time.Now().Unix() > expiresAt {
		writeJSONError(w, http.StatusForbidden, "upload link expired")
		return
	}

	// 2. Enforce Content-Type (Strict Check)
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = drivers.DefaultMime
	}
	if contentType != encodedContentType {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content-type mismatch")
		return
	}

	// 3. Prevent Local Disk Exhaustion (DoS) - enforce dynamic limit from URL
	r.Body = http.MaxBytesReader(w, r.Body, maxSizeBytes)

	err = h.driver.Save(r.Context(), key, r.Body, contentType)
	if err != nil {
		slog.ErrorContext(r.Context(), "local upload failed", "key", key, "error", err)
		// MaxBytesReader returns a specific error when exceeded
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "file size exceeds specified limit")
		} else {
			writeJSONError(w, http.StatusInternalServerError, "failed to save file")
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// download streams the file for a download URL issued by
// GetDownloadURL.
func (h *LocalContentHandler) download(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "key is required")
		return
	}
	if !validStorageKey(key) {
		writeJSONError(w, http.StatusBadRequest, "invalid key format")
		return
	}

	// Extract and verify security constraints
	token := r.URL.Query().Get("token")
	expiresAtStr := r.URL.Query().Get("expiresAt")
	if token == "" || expiresAtStr == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing security token or expiration")
		return
	}

	expiresAt, err := strconv.ParseInt(expiresAtStr, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid expiration format")
		return
	}

	// Verify HMAC signature
	if !h.driver.VerifyDownloadToken(key, token, expiresAt) {
		writeJSONError(w, http.StatusUnauthorized, "invalid security token")
		return
	}

	// Enforce TTL
	if time.Now().Unix() > expiresAt {
		writeJSONError(w, http.StatusForbidden, "download link expired")
		return
	}

	body, contentType, err := h.driver.Get(r.Context(), key)
	if err != nil {
		slog.ErrorContext(r.Context(), "download content failed", "key", key, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to get file")
		return
	}
	defer func() { _ = body.Close() }()

	w.Header().Set("Content-Type", contentType)
	// The file is served from the API's own origin, so stop the browser from
	// sniffing it into something executable, and sandbox anything rendered
	// inline so an uploaded HTML or SVG file cannot run script here. This
	// applies to PDFs too: Chromium still displays a PDF served with a
	// sandbox policy (see its CSPWithSandboxDoesNotBlockPDF browser test).
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	// Check if the body can report its size (standard for files/drivers)
	w.Header().Set("Content-Disposition", "inline")
	if stater, ok := body.(interface{ Stat() (os.FileInfo, error) }); ok {
		if fi, err := stater.Stat(); err == nil {
			w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
		}
	}

	// Ensure headers (including Content-Length) are written before the body so
	// that browsers can correctly display download progress.
	w.WriteHeader(http.StatusOK)

	_, err = io.Copy(w, body)
	if err != nil {
		slog.ErrorContext(r.Context(), "Failed to stream download content", "key", key, "error", err)
	}
}
