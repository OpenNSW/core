package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"

	"github.com/OpenNSW/core/httputil"
	corestorage "github.com/OpenNSW/core/storage"

	"github.com/OpenNSW/core/platform/authn"
	"github.com/OpenNSW/nsw-srilanka/internal/storage/fileaccess"
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
	h.serveUpload(w, r, h.uploads)
}

// InternalUpload is Upload for the files a machine client generates and stores
// itself, such as an agency's documents, which may be of types users may not
// upload. Its reference opens the file as well as being submittable. Which
// callers reach it is the route's scope: it does not check the kind of principal.
func (h *FileHandler) InternalUpload(w http.ResponseWriter, r *http.Request) {
	h.serveUpload(w, r, h.machine)
}

// serveUpload prepares an upload with up and answers with a reference to it.
func (h *FileHandler) serveUpload(w http.ResponseWriter, r *http.Request, up Uploader) {
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

	meta, err := up.Upload(r.Context(), req.Filename, req.Size, req.MimeType)
	if err != nil {
		h.writeServiceError(w, r, err, "failed to prepare upload", req.MimeType)
		return
	}
	ref, err := h.refs.Issue(p, meta.Key, fileaccess.UploadScopes(p)...)
	if err != nil {
		httputil.InternalServerError(w, r, "storage: failed to issue a file reference", err)
		return
	}
	httputil.JSON(w, http.StatusOK, uploadResponse{
		Key:       ref,
		Name:      meta.Name,
		UploadURL: meta.UploadURL,
		Size:      meta.Size,
		MimeType:  meta.MimeType,
	})
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
	case errors.Is(err, fileaccess.ErrInvalid), errors.Is(err, fileaccess.ErrNotYours):
		// One answer for both, so the route says nothing about a value it
		// will not serve. A raw storage key is one: it grants nothing.
		httputil.Error(w, r, http.StatusForbidden, "this is not a file reference issued to you")
		return
	case errors.Is(err, fileaccess.ErrScope):
		httputil.Error(w, r, http.StatusForbidden, "this file reference can't be used to open the file; save the form to view it")
		return
	case errors.Is(err, fileaccess.ErrExpired):
		httputil.Error(w, r, http.StatusGone, "this file reference has expired; reload to get a new one")
		return
	default:
		httputil.InternalServerError(w, r, "storage: failed to resolve a file reference", err)
		return
	}

	dl, err := downloadOf(r.Context(), h.svc, value)
	if err != nil {
		h.writeServiceError(w, r, err, "failed to generate access", "")
		return
	}
	httputil.JSON(w, http.StatusOK, dl)
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
