package storage

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/OpenNSW/core/httputil"
)

// maxUploadRequestBytes caps the upload request body: it carries only the
// file's metadata, never its content.
const maxUploadRequestBytes = 1 << 20

// ProxyHandler serves the storage API routes in proxy mode, with the same
// request and response shapes as core/storage's HTTPHandler. File-level
// rules — allowed types, the size limit, the key format — are the owning
// service's: it enforces them, and its rejections are relayed to the client.
//
// Authentication is the route middleware's job; the handler assumes the
// caller has already been authenticated and authorized.
type ProxyHandler struct {
	svc *ProxyService
}

// NewProxyHandler creates a ProxyHandler over svc.
func NewProxyHandler(svc *ProxyService) *ProxyHandler {
	return &ProxyHandler{svc: svc}
}

// Upload reserves a key with the owning service and returns the file metadata,
// including the URL the client uploads the file to.
func (h *ProxyHandler) Upload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filename string `json:"filename"`
		MimeType string `json:"mime_type"`
		Size     int64  `json:"size"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Filename == "" {
		httputil.Error(w, r, http.StatusBadRequest, "filename is required")
		return
	}
	if req.MimeType == "" {
		httputil.Error(w, r, http.StatusBadRequest, "mime_type is required")
		return
	}
	if req.Size <= 0 {
		httputil.Error(w, r, http.StatusBadRequest, "size must be greater than 0")
		return
	}

	meta, err := h.svc.Upload(r.Context(), req.Filename, req.Size, req.MimeType)
	if err != nil {
		writeProxyError(w, r, err, "failed to prepare upload")
		return
	}
	httputil.JSON(w, http.StatusOK, meta)
}

// Download returns the owning service's download URL for the key and when it
// expires, with the type of the file and how the URL serves it as the owning
// service reported them.
func (h *ProxyHandler) Download(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		httputil.Error(w, r, http.StatusBadRequest, "key is required")
		return
	}

	dl, err := h.svc.DownloadURL(r.Context(), key)
	if err != nil {
		writeProxyError(w, r, err, "failed to generate access")
		return
	}
	body := map[string]any{
		"download_url": dl.URL,
		"expires_at":   dl.ExpiresAt,
		"mime_type":    dl.ContentType,
	}
	// An owning service that says nothing about it leaves the field out, which a
	// client reads as a file to save.
	if dl.Disposition != "" {
		body["disposition"] = dl.Disposition
	}
	httputil.JSON(w, http.StatusOK, body)
}

// Delete removes the file from the owning service.
func (h *ProxyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		httputil.Error(w, r, http.StatusBadRequest, "key is required")
		return
	}

	if err := h.svc.Delete(r.Context(), key); err != nil {
		writeProxyError(w, r, err, "failed to delete file")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeProxyError relays a rejection of the request by the owning service
// with its status and message; anything else — the owning service being
// unreachable, failing, or refusing this service's credentials — is a
// gateway failure the client cannot fix.
func writeProxyError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	var rejection *RemoteRejection
	if errors.As(err, &rejection) {
		message := rejection.Message
		if message == "" {
			message = fallback
		}
		httputil.Error(w, r, rejection.StatusCode, message)
		return
	}
	slog.ErrorContext(r.Context(), "storage proxy request failed", "error", err)
	httputil.Error(w, r, http.StatusBadGateway, fallback)
}
