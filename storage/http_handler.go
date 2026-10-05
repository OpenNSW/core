// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/OpenNSW/core/authn"
)

var allowedContentTypes = map[string]struct{}{
	"application/pdf": {},
	"image/jpeg":      {},
	"image/png":       {},
	"image/gif":       {},
	"image/webp":      {},
	// .xlsx only: the OOXML spreadsheet format cannot carry VBA macros
	// (macro-enabled workbooks use .xlsm), unlike legacy .xls which is a
	// known malware vector and stays prohibited.
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": {},
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
	if authn.GetAuthContext(r.Context()) == nil {
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
		slog.ErrorContext(r.Context(), "upload preparation failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to prepare upload")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metadata); err != nil {
		slog.ErrorContext(r.Context(), "Failed to encode response", "error", err)
	}
}

func (h *HTTPHandler) Download(w http.ResponseWriter, r *http.Request) {
	// TODO: Uncomment when M2M AUTH Implemented.
	//if authn.GetAuthContext(r.Context()) == nil {
	//	slog.WarnContext(r.Context(), "authentication required but not provided for download")
	//	writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
	//	return
	//}

	key := r.PathValue("key")
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "key is required")
		return
	}

	url, expiresAt, err := h.Service.DownloadURL(r.Context(), key)
	if errors.Is(err, ErrInvalidKey) {
		writeJSONError(w, http.StatusBadRequest, "invalid key format")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "Failed to generate download URL", "key", key, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to generate access")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"download_url": url,
		"expires_at":   expiresAt,
	}); err != nil {
		slog.ErrorContext(r.Context(), "Failed to encode response", "error", err)
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

	err := h.Service.Delete(r.Context(), key)
	if errors.Is(err, ErrInvalidKey) {
		writeJSONError(w, http.StatusBadRequest, "invalid key format")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "Delete failed", "error", err, "key", key)
		writeJSONError(w, http.StatusInternalServerError, "failed to delete file")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
