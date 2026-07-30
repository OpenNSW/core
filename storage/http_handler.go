// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/OpenNSW/core/authn"
	"github.com/OpenNSW/core/storage/drivers"
)

// validStorageKey returns true if key matches UUID or UUID plus extension (e.g. .pdf).
var storageKeyRx = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(\.[a-zA-Z0-9]+)?$`)

func validStorageKey(key string) bool {
	return len(key) >= 36 && storageKeyRx.MatchString(key)
}

var allowedContentTypes = map[string]struct{}{
	"application/pdf": {},
	"image/jpeg":      {},
	"image/png":       {},
	"image/gif":       {},
	"image/webp":      {},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": {},
}

func isAllowedContentType(ct string) bool {
	_, ok := allowedContentTypes[ct]
	return ok
}

type AccessValidator func(ctx context.Context, key string, authCtx *authn.AuthContext) (bool, error)
type OnUploadHook func(ctx context.Context, metadata *FileMetadata, authCtx *authn.AuthContext) error

type HTTPHandler struct {
	Service         *Service
	AccessValidator AccessValidator
	OnUploadHook    OnUploadHook
}

func NewHTTPHandler(service *Service) *HTTPHandler {
	return &HTTPHandler{Service: service}
}

func (h *HTTPHandler) WithAccessValidator(fn AccessValidator) *HTTPHandler {
	h.AccessValidator = fn
	return h
}

func (h *HTTPHandler) WithOnUploadHook(fn OnUploadHook) *HTTPHandler {
	h.OnUploadHook = fn
	return h
}

// writeJSONError sets Content-Type: application/json and writes a consistent JSON error body.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (h *HTTPHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if authn.GetAuthContext(r.Context()) == nil {
		slog.WarnContext(r.Context(), "authentication required but not provided for upload")
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	// New Presigned URL generation flow (application/json)
	var req struct {
		Filename  string         `json:"filename"`
		MimeType  string         `json:"mime_type"`
		Size      int64          `json:"size"`
		Ownership map[string]any `json:"ownership,omitempty"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Filename == "" {
		writeJSONError(w, http.StatusBadRequest, "filename is required")
		return
	}
	if req.MimeType == "" {
		writeJSONError(w, http.StatusBadRequest, "mime_type is required")
		return
	}
	if req.Size <= 0 {
		writeJSONError(w, http.StatusBadRequest, "size must be greater than 0")
		return
	}

	if req.Size > 32<<20 {
		writeJSONError(w, http.StatusBadRequest, "file size exceeds 32MB limit")
		return
	}

	if !isAllowedContentType(req.MimeType) {
		writeJSONError(w, http.StatusUnsupportedMediaType, "invalid or prohibited file type")
		return
	}

	cleanName, err := CleanFilename(req.Filename)
	if err != nil {
		slog.WarnContext(r.Context(), "invalid or prohibited filename", "filename", req.Filename, "error", err)
		writeJSONError(w, http.StatusUnsupportedMediaType, "prohibited file extension or invalid filename")
		return
	}
	req.Filename = cleanName

	metadata, err := h.Service.Upload(r.Context(), req.Filename, req.Size, req.MimeType)
	if err != nil {
		slog.ErrorContext(r.Context(), "upload preparation failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to prepare upload")
		return
	}

	if len(req.Ownership) > 0 {
		metadata.Ownership = req.Ownership
	}

	if h.OnUploadHook != nil {
		if err := h.OnUploadHook(r.Context(), metadata, authn.GetAuthContext(r.Context())); err != nil {
			slog.ErrorContext(r.Context(), "OnUploadHook callback failed", "key", metadata.Key, "error", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	if err := json.NewEncoder(w).Encode(metadata); err != nil {
		slog.ErrorContext(r.Context(), "Failed to encode response", "error", err)
	}
}

// UploadContentLocal acts as a mock S3 bucket for local development.
// It accepts a PUT request with the raw file body.
func (h *HTTPHandler) UploadContentLocal(w http.ResponseWriter, r *http.Request) {
	// This endpoint is only available when using LocalFSDriver (local development).
	driver, ok := h.Service.Driver.(*drivers.LocalFSDriver)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	if r.Method != http.MethodPut {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

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
	if !driver.VerifyToken(key, token, expiresAt, encodedContentType, maxSizeBytes) {
		writeJSONError(w, http.StatusUnauthorized, "invalid security token")
		return
	}

	// 1. Enforce TTL (Time-To-Live)
	if time.Now().Unix() > expiresAt {
		writeJSONError(w, http.StatusForbidden, "upload link expired")
		return
	}

	// 2. Enforce Content-Type (Strict Check)
	var contentType string
	contentType = r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = drivers.DefaultMime
	}
	if contentType != encodedContentType {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content-type mismatch")
		return
	}

	// 3. Prevent Local Disk Exhaustion (DoS) - enforce dynamic limit from URL
	r.Body = http.MaxBytesReader(w, r.Body, maxSizeBytes)

	// Magic Bytes Inspection: Read and validate header (first 512 bytes)
	headerBuf := make([]byte, 512)
	n, readErr := io.ReadFull(r.Body, headerBuf)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		var maxBytesError *http.MaxBytesError
		if errors.As(readErr, &maxBytesError) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "file size exceeds specified limit")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "failed to read file header")
		return
	}
	headerBytes := headerBuf[:n]

	if err := ValidateHeader(headerBytes, contentType); err != nil {
		slog.WarnContext(r.Context(), "file content validation failed", "key", key, "error", err)
		writeJSONError(w, http.StatusUnsupportedMediaType, "file content does not match declared type or contains prohibited data")
		return
	}

	// For text-like MIME types, validate full payload to ensure prohibited markup is not hidden after byte 512
	cleanContentType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	var combinedBody io.Reader
	if cleanContentType == "text/plain" || cleanContentType == "text/csv" || cleanContentType == "application/json" {
		restBytes, restErr := io.ReadAll(r.Body)
		if restErr != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(restErr, &maxBytesError) {
				writeJSONError(w, http.StatusRequestEntityTooLarge, "file size exceeds specified limit")
				return
			}
			writeJSONError(w, http.StatusBadRequest, "failed to read file content")
			return
		}
		fullBytes := append(headerBytes, restBytes...)
		if err := ValidateTextContent(fullBytes); err != nil {
			slog.WarnContext(r.Context(), "file content validation failed for text body", "key", key, "error", err)
			writeJSONError(w, http.StatusUnsupportedMediaType, "file content does not match declared type or contains prohibited data")
			return
		}
		combinedBody = bytes.NewReader(fullBytes)
	} else {
		combinedBody = io.MultiReader(bytes.NewReader(headerBytes), r.Body)
	}

	// Save using the local driver
	err = driver.Save(r.Context(), key, combinedBody, contentType)
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

func (h *HTTPHandler) Download(w http.ResponseWriter, r *http.Request) {
	if authn.GetAuthContext(r.Context()) == nil {
		slog.WarnContext(r.Context(), "authentication required but not provided for download")
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	key := r.PathValue("key")
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "key is required")
		return
	}
	if !validStorageKey(key) {
		writeJSONError(w, http.StatusBadRequest, "invalid key format")
		return
	}

	if h.AccessValidator != nil {
		allowed, err := h.AccessValidator(r.Context(), key, authn.GetAuthContext(r.Context()))
		if err != nil {
			slog.ErrorContext(r.Context(), "Failed to validate access for key", "key", key, "error", err)
			writeJSONError(w, http.StatusInternalServerError, "failed to validate access")
			return
		}
		if !allowed {
			slog.WarnContext(r.Context(), "Access denied by storage AccessValidator", "key", key)
			writeJSONError(w, http.StatusForbidden, "forbidden")
			return
		}
	}

	url, err := h.Service.GetDownloadURL(r.Context(), key)
	if err != nil {
		slog.ErrorContext(r.Context(), "Failed to generate download URL", "key", key, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to generate access")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"download_url": url,
		"expires_at":   time.Now().Add(drivers.DefaultPresignTTL).Unix(),
	}); err != nil {
		slog.ErrorContext(r.Context(), "Failed to encode response", "error", err)
	}
}

// DownloadContent streams the file body directly from the local filesystem driver.
// It is intended only for local development when using LocalFSDriver; in non-local
// environments (e.g. S3) callers should use GetDownloadURL and presigned URLs instead.
func (h *HTTPHandler) DownloadContent(w http.ResponseWriter, r *http.Request) {
	// This endpoint is only available when using LocalFSDriver (local development).
	// It serves the same role as an S3 presigned URL — no auth required since the
	// caller was already authenticated when obtaining the URL via GET /uploads/{key}.
	driver, ok := h.Service.Driver.(*drivers.LocalFSDriver)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

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
	if !driver.VerifyDownloadToken(key, token, expiresAt) {
		writeJSONError(w, http.StatusUnauthorized, "invalid security token")
		return
	}

	// Enforce TTL
	if time.Now().Unix() > expiresAt {
		writeJSONError(w, http.StatusForbidden, "download link expired")
		return
	}

	body, contentType, err := h.Service.Download(r.Context(), key)
	if err != nil {
		slog.ErrorContext(r.Context(), "download content failed", "key", key, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to get file")
		return
	}
	defer func() { _ = body.Close() }()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("X-Content-Type-Options", "nosniff")
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

func (h *HTTPHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if authn.GetAuthContext(r.Context()) == nil {
		slog.WarnContext(r.Context(), "authentication required but not provided for delete")
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	key := r.PathValue("key")
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "key is required")
		return
	}
	if !validStorageKey(key) {
		writeJSONError(w, http.StatusBadRequest, "invalid key format")
		return
	}

	if err := h.Service.Delete(r.Context(), key); err != nil {
		slog.ErrorContext(r.Context(), "Delete failed", "error", err, "key", key)
		writeJSONError(w, http.StatusInternalServerError, "failed to delete file")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
