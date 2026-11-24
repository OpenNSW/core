// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/OpenNSW/core/httputil"
	corestorage "github.com/OpenNSW/core/storage"

	"github.com/OpenNSW/core/platform/audit"
	"github.com/OpenNSW/core/platform/authn"
	"github.com/OpenNSW/core/platform/storage/fileaccess"
)

// FileRefs issues the file references callers hold in place of stored values,
// and resolves them back. *fileaccess.Access satisfies it, and its errors are
// the ones FileHandler maps.
type FileRefs interface {
	Issue(p *authn.Principal, value string, scopes ...fileaccess.Scope) (string, error)
	Resolve(p *authn.Principal, ref string, need fileaccess.Scope) (string, error)
}

// FileHandler serves the storage routes, in either mode: an upload returns a
// file reference issued to the caller, and a download resolves one for the
// caller before asking Service for the file. A stored value is never returned
// to the caller, though a presigned upload URL may name it: it grants nothing
// on its own.
//
// Authentication is the route middleware's job; the handler only reads the
// principal it attached.
type FileHandler struct {
	svc  Service
	refs FileRefs
	// uploads prepares the uploads on the user route: svc unless an option
	// says otherwise.
	uploads Uploader
	// machine prepares the uploads on the internal route. Nil when this
	// deployment serves none.
	machine Uploader
	// strictBodies makes an upload request refuse what its contract
	// otherwise ignores; see WithStrictRequestBodies.
	strictBodies bool
	// audit records each upload prepare (PRESIGN_UPLOAD) and each download
	// link issued or refused (READ). Nil skips auditing.
	audit audit.Auditor
}

// FileHandlerOption adjusts a FileHandler.
type FileHandlerOption func(*FileHandler)

// WithUploads prepares the uploads on the user route with u instead of the
// Service.
func WithUploads(u Uploader) FileHandlerOption {
	return func(h *FileHandler) { h.uploads = u }
}

// WithMachineUploads enables InternalUpload, preparing its uploads with u.
func WithMachineUploads(u Uploader) FileHandlerOption {
	return func(h *FileHandler) { h.machine = u }
}

// WithStrictRequestBodies makes the upload routes read their request as
// httputil.DecodeJSON does by default: a field the request does not declare,
// or anything after the JSON value, is a 400, and a request over the limit a
// 413. Without it, unknown fields and trailing data are ignored, and any
// request that can't be read is a 400.
func WithStrictRequestBodies() FileHandlerOption {
	return func(h *FileHandler) { h.strictBodies = true }
}

// WithAuditor records each upload prepare as a STORAGE PRESIGN_UPLOAD audit
// event and each download link issued or refused as a STORAGE READ event. Nil
// disables auditing.
func WithAuditor(a audit.Auditor) FileHandlerOption {
	return func(h *FileHandler) { h.audit = a }
}

// NewFileHandler creates a FileHandler over svc, resolving references with
// refs.
func NewFileHandler(svc Service, refs FileRefs, opts ...FileHandlerOption) *FileHandler {
	h := &FileHandler{svc: svc, refs: refs, uploads: svc}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// HasMachineUploads reports whether InternalUpload can serve requests.
func (h *FileHandler) HasMachineUploads() bool {
	return h.machine != nil
}

// uploadResponse is what an upload returns: the reference to keep for the
// file, and where to upload it.
type uploadResponse struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	UploadURL string `json:"upload_url"`
	Size      int64  `json:"size"`
	MimeType  string `json:"mime_type"`
}

// Upload reserves a stored file and returns a reference to it for the caller,
// with the URL to upload the file to. A user's reference can only be submitted,
// as the value of a form's file field, so it never expires; a machine
// client's, such as an agency proxying its officer's upload, opens the file too.
func (h *FileHandler) Upload(w http.ResponseWriter, r *http.Request) {
	h.serveUpload(w, r, h.uploads, "user")
}

// InternalUpload is Upload for the files a machine client generates and stores
// itself, such as an agency's documents, which may be of types users may not
// upload. Its reference opens the file as well as being submittable. Which
// callers reach it is the route's scope: it does not check the kind of principal.
func (h *FileHandler) InternalUpload(w http.ResponseWriter, r *http.Request) {
	h.serveUpload(w, r, h.machine, "machine")
}

// decodeUploadRequest reads an upload request into req, within
// maxUploadRequestBytes, by the rules WithStrictRequestBodies chooses. On
// failure it writes the response and returns false.
func (h *FileHandler) decodeUploadRequest(w http.ResponseWriter, r *http.Request, req any) bool {
	if h.strictBodies {
		if err := httputil.DecodeJSON(w, r, maxUploadRequestBytes, req); err != nil {
			httputil.WriteDecodeError(w, r, err)
			return false
		}
		return true
	}
	if err := httputil.DecodeJSON(w, r, maxUploadRequestBytes, req, httputil.AllowUnknownFields(), httputil.AllowTrailingData()); err != nil {
		httputil.Error(w, r, http.StatusBadRequest, httputil.MsgInvalidBody)
		return false
	}
	return true
}

// serveUpload prepares an upload with up and answers with a reference to it.
// kind is "user" or "machine" and is recorded on the prepare log and audit line.
func (h *FileHandler) serveUpload(w http.ResponseWriter, r *http.Request, up Uploader, kind string) {
	p, ok := authn.FromContext(r.Context())
	if !ok {
		httputil.Error(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		Filename string `json:"filename"`
		MimeType string `json:"mime_type"`
		Size     int64  `json:"size"`
	}
	if !h.decodeUploadRequest(w, r, &req) {
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

	meta, err := up.Upload(r.Context(), req.Filename, req.Size, req.MimeType)
	if err != nil {
		h.recordUploadPrepare(r.Context(), p, kind, req.Filename, req.MimeType, req.Size, "", err)
		h.writeServiceError(w, r, err, "failed to prepare upload", req.MimeType)
		return
	}
	ref, err := h.refs.Issue(p, meta.Key, fileaccess.UploadScopes(p)...)
	if err != nil {
		h.recordUploadPrepare(r.Context(), p, kind, req.Filename, req.MimeType, req.Size, h.recordedKey(meta.Key), err)
		httputil.InternalServerError(w, r, "storage: failed to issue a file reference", err)
		return
	}
	h.recordUploadPrepare(r.Context(), p, kind, req.Filename, req.MimeType, req.Size, h.recordedKey(meta.Key), nil)
	httputil.JSON(w, http.StatusOK, uploadResponse{
		Key:       ref,
		Name:      meta.Name,
		UploadURL: meta.UploadURL,
		Size:      meta.Size,
		MimeType:  meta.MimeType,
	})
}

// recordedKey returns the identifier of a stored file that logs and audit
// records carry. In proxy mode the stored value is the owning service's file
// reference, which grants access there, so it is recorded as the hex SHA-256 of
// the reference; the owning service can record the same hash of the reference
// it resolves, and the two records of one request then match. Otherwise the
// value is an opaque storage key and is recorded as it is.
func (h *FileHandler) recordedKey(value string) string {
	if value == "" {
		return ""
	}
	if _, proxied := h.svc.(fileDownloader); !proxied {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// recordUploadPrepare writes a system slog line and a STORAGE PRESIGN_UPLOAD
// audit event for an upload prepare. It names mime, size, extension and
// storageKey (see recordedKey) — not the filename or the file reference issued
// to the caller.
func (h *FileHandler) recordUploadPrepare(
	ctx context.Context, p *authn.Principal, kind, filename, mimeType string, size int64, storageKey string, err error,
) {
	ext := strings.ToLower(path.Ext(filename))
	attrs := []any{
		"kind", kind,
		"mime_type", mimeType,
		"size", size,
	}
	if ext != "" {
		attrs = append(attrs, "extension", ext)
	}
	if p != nil && p.ClientID != "" {
		attrs = append(attrs, "client_id", p.ClientID)
	}
	if storageKey != "" {
		attrs = append(attrs, "storage_key", storageKey)
	}
	if err != nil {
		attrs = append(attrs, "error", err)
		slog.WarnContext(ctx, "storage: upload not prepared", attrs...)
	} else {
		slog.InfoContext(ctx, "storage: upload prepared", attrs...)
	}

	meta := map[string]any{
		"kind":      kind,
		"mime_type": mimeType,
		"size":      size,
	}
	if ext != "" {
		meta["extension"] = ext
	}
	if p != nil && p.ClientID != "" {
		meta["client_id"] = p.ClientID
	}
	if err != nil {
		meta["error"] = err.Error()
	}
	if h.audit != nil {
		h.audit.Audit(ctx, audit.StorageEvent(audit.ActionPresignUpload, storageKey, err != nil, meta))
	}
}

// Download returns a time-limited URL to download the file the reference in
// the path stands for, and when it expires.
func (h *FileHandler) Download(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("key")
	if ref == "" {
		httputil.Error(w, r, http.StatusBadRequest, "key is required")
		return
	}
	p, ok := authn.FromContext(r.Context())
	if !ok {
		httputil.Error(w, r, http.StatusUnauthorized, "authentication required")
		return
	}

	value, err := h.refs.Resolve(p, ref, fileaccess.ScopeRead)
	switch {
	case err == nil:
	case errors.Is(err, fileaccess.ErrInvalid):
		// One answer for ErrInvalid and ErrNotYours, so the route says nothing
		// about a value it will not serve. A raw storage key is one: it grants
		// nothing. The audit record still tells them apart.
		h.recordDownload(r.Context(), p, "", "invalid_reference", err, 0)
		httputil.Error(w, r, http.StatusForbidden, "this is not a file reference issued to you")
		return
	case errors.Is(err, fileaccess.ErrNotYours):
		h.recordDownload(r.Context(), p, "", "not_yours", err, 0)
		httputil.Error(w, r, http.StatusForbidden, "this is not a file reference issued to you")
		return
	case errors.Is(err, fileaccess.ErrScope):
		h.recordDownload(r.Context(), p, "", "wrong_scope", err, 0)
		httputil.Error(w, r, http.StatusForbidden, "this file reference can't be used to open the file; save the form to view it")
		return
	case errors.Is(err, fileaccess.ErrExpired):
		h.recordDownload(r.Context(), p, "", "expired", err, 0)
		httputil.Error(w, r, http.StatusGone, "this file reference has expired; reload to get a new one")
		return
	default:
		h.recordDownload(r.Context(), p, "", "resolve_failed", err, 0)
		httputil.InternalServerError(w, r, "storage: failed to resolve a file reference", err)
		return
	}

	key := h.recordedKey(value)
	dl, err := downloadOf(r.Context(), h.svc, value)
	if err != nil {
		h.recordDownload(r.Context(), p, key, downloadFailureReason(err), err, 0)
		h.writeServiceError(w, r, err, "failed to generate access", "")
		return
	}
	h.recordDownload(r.Context(), p, key, "", nil, dl.ExpiresAt)
	httputil.JSON(w, http.StatusOK, dl)
}

// downloadFailureReason names why the storage service could not give a link
// for a reference that did resolve.
func downloadFailureReason(err error) string {
	var rejection *RemoteRejection
	switch {
	case errors.Is(err, corestorage.ErrNotFound),
		errors.As(err, &rejection) && rejection.StatusCode == http.StatusNotFound:
		return "not_found"
	case errors.Is(err, corestorage.ErrInvalidKey):
		return "invalid_key"
	case isUpstreamFailure(err):
		return "upstream_failed"
	default:
		return "failed"
	}
}

// recordDownload writes a system slog line and a STORAGE READ audit event for a
// download link issued (err nil) or refused. storageKey is the recorded key
// (see recordedKey) and is empty when the reference did not resolve: a refused
// reference is never recorded. reason names why a link was refused; expiresAt
// is the issued link's expiry in Unix seconds.
func (h *FileHandler) recordDownload(
	ctx context.Context, p *authn.Principal, storageKey, reason string, err error, expiresAt int64,
) {
	attrs := []any{}
	meta := map[string]any{}
	if storageKey != "" {
		attrs = append(attrs, "storage_key", storageKey)
	}
	if p != nil && p.ClientID != "" {
		attrs = append(attrs, "client_id", p.ClientID)
		meta["client_id"] = p.ClientID
	}
	if err != nil {
		attrs = append(attrs, "reason", reason, "error", err)
		meta["reason"] = reason
		meta["error"] = err.Error()
		slog.WarnContext(ctx, "storage: download link refused", attrs...)
	} else {
		attrs = append(attrs, "expires_at", expiresAt)
		meta["expires_at"] = expiresAt
		slog.InfoContext(ctx, "storage: download link issued", attrs...)
	}
	if h.audit != nil {
		h.audit.Audit(ctx, audit.StorageEvent(audit.ActionRead, storageKey, err != nil, meta))
	}
}

// fileDownload is what a download returns: a time-limited URL to the file,
// when it expires, and what the file is and how the URL serves it. The client
// holds only a reference, so it has no other way to name a saved file it saves
// or to tell whether a browser may open it. The name the file was uploaded
// under is not kept, so only its extension is given; the storage key it comes
// from is not.
type fileDownload struct {
	DownloadURL string `json:"download_url"`
	ExpiresAt   int64  `json:"expires_at"`
	// Extension is the file's, with its dot (".xlsx"), or empty when it has
	// none. It names a file the client saves and tells nothing about its type.
	Extension string `json:"extension,omitempty"`
	// MimeType is the type the file was stored with.
	MimeType string `json:"mime_type,omitempty"`
	// Disposition is how the URL serves the file: "inline" when a browser may
	// open it, "attachment" when it only saves it. It is empty when the
	// service that holds the file does not say.
	Disposition string `json:"disposition,omitempty"`
}

// fileDownloader is a Service that describes the file behind a stored value
// itself. ProxyService is one: what it stores is the owning service's
// reference, so only the owning service knows the file it stands for.
type fileDownloader interface {
	fileDownload(ctx context.Context, key string) (fileDownload, error)
}

// downloadOf returns the download of the file stored under value. Unless svc
// describes the file itself, value is a storage key, which core/storage
// builds from the uploaded file's name to keep its extension.
func downloadOf(ctx context.Context, svc Service, value string) (fileDownload, error) {
	if d, ok := svc.(fileDownloader); ok {
		return d.fileDownload(ctx, value)
	}
	dl, err := svc.DownloadURL(ctx, value)
	if err != nil {
		return fileDownload{}, err
	}
	return fileDownload{
		DownloadURL: dl.URL,
		ExpiresAt:   dl.ExpiresAt,
		Extension:   path.Ext(value),
		MimeType:    dl.ContentType,
		Disposition: dl.Disposition,
	}, nil
}

// writeServiceError answers a failed Service call the way core/storage's and
// the proxy's own handlers do. mimeType is the type an upload asked for, named
// when it is refused so the uploader can see what their file was sent as; it
// is empty for a download.
func (h *FileHandler) writeServiceError(w http.ResponseWriter, r *http.Request, err error, fallback, mimeType string) {
	var tooLarge *corestorage.FileTooLargeError
	var mismatch *corestorage.ExtensionMismatchError
	switch {
	case errors.Is(err, corestorage.ErrInvalidKey):
		httputil.Error(w, r, http.StatusBadRequest, "invalid key format")
	case errors.Is(err, corestorage.ErrNotFound):
		httputil.Error(w, r, http.StatusNotFound, "file not found")
	case errors.Is(err, corestorage.ErrInvalidSize):
		httputil.Error(w, r, http.StatusBadRequest, "size must be greater than 0")
	case errors.As(err, &tooLarge):
		httputil.Error(w, r, http.StatusBadRequest, fmt.Sprintf("file size exceeds %s limit", formatSize(tooLarge.Limit)))
	case errors.Is(err, corestorage.ErrContentTypeNotAllowed):
		message := "invalid or prohibited file type"
		if mimeType != "" {
			message += ": " + mimeType
		}
		httputil.Error(w, r, http.StatusUnsupportedMediaType, message)
	case errors.As(err, &mismatch):
		httputil.Error(w, r, http.StatusBadRequest, fmt.Sprintf("the file name's extension (%q) does not match its type (%s)", mismatch.Extension, mismatch.MediaType))
	case isUpstreamFailure(err):
		writeProxyError(w, r, err, fallback)
	default:
		httputil.InternalServerError(w, r, "storage: "+fallback, err)
	}
}

// isUpstreamFailure reports errors from a remote storage service, including
// rejections that should be relayed. The HTTP handler maps the error's meaning
// rather than inspecting which Service implementation returned it.
func isUpstreamFailure(err error) bool {
	var rejection *RemoteRejection
	var failure *upstreamFailure
	return errors.As(err, &rejection) || errors.As(err, &failure)
}

// formatSize writes a byte count the way core/storage's messages do.
func formatSize(n int64) string {
	if n%(1<<20) == 0 {
		return fmt.Sprintf("%dMB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}
