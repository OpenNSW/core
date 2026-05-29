package storage

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/OpenNSW/nsw/backend/internal/auth"
	"github.com/OpenNSW/nsw/backend/pkg/storage/drivers"
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
}

func isAllowedContentType(ct string) bool {
	_, ok := allowedContentTypes[ct]
	return ok
}

type HTTPHandler struct {
	Service *Service
}

func NewHTTPHandler(service *Service) *HTTPHandler {
	return &HTTPHandler{Service: service}
}

// writeJSONError sets Content-Type: application/json and writes a consistent JSON error body.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (h *HTTPHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if auth.GetAuthContext(r.Context()) == nil {
		slog.WarnContext(r.Context(), "authentication required but not provided for upload")
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	// New Presigned URL generation flow (application/json)
	var req struct {
		Filename string `json:"filename"`
		MimeType string `json:"mime_type"`
		Size     int64  `json:"size"`
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

	metadata, err := h.Service.Upload(r.Context(), req.Filename, req.Size, req.MimeType)
	if err != nil {
		slog.ErrorContext(r.Context(), "Upload preparation failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to prepare upload")
		return
	}

	w.Header().Set("Content-Type", "application/json")
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

	// Save using the local driver
	err = driver.Save(r.Context(), key, r.Body, contentType)
	if err != nil {
		slog.ErrorContext(r.Context(), "Local upload failed", "key", key, "error", err)
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
	// TODO: Uncomment when M2M AUTH Implemented.
	//if auth.GetAuthContext(r.Context()) == nil {
	//	slog.WarnContext(r.Context(), "authentication required but not provided for download")
	//	writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
	//	return
	//}

	key := r.PathValue("key")
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "key is required")
		return
	}
	if !validStorageKey(key) {
		writeJSONError(w, http.StatusBadRequest, "invalid key format")
		return
	}

	url, err := h.Service.GetDownloadURL(r.Context(), key)
	if err != nil {
		slog.ErrorContext(r.Context(), "Failed to generate download URL", "key", key, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to generate access")
		return
	}

	w.Header().Set("Content-Type", "application/json")
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
		slog.ErrorContext(r.Context(), "Download content failed", "key", key, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to get file")
		return
	}
	defer func() { _ = body.Close() }()

	w.Header().Set("Content-Type", contentType)
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

func (h *HTTPHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if auth.GetAuthContext(r.Context()) == nil {
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
