package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OpenNSW/core/remote"
	corestorage "github.com/OpenNSW/core/storage"
)

// proxyFetchTimeout bounds fetching a presigned download URL, including
// streaming its body.
const proxyFetchTimeout = 60 * time.Second

// maxProxyErrorBody caps how much of a failed download's body is read into
// an error message.
const maxProxyErrorBody = 4 << 10

// ServiceCaller calls a service in the outbound services registry;
// *remote.Manager satisfies it.
type ServiceCaller interface {
	GetClient(id string) (*remote.Client, error)
	Call(ctx context.Context, serviceID string, req remote.Request, response any) error
}

// ProxyService is a Service that holds no files: every operation is
// forwarded to the storage API of the service that owns them, which also
// allocates the keys. The key it returns from Upload is the owning service's,
// and is the one to persist.
type ProxyService struct {
	caller ServiceCaller
	cfg    ProxyConfig
	// fetchClient fetches presigned download URLs. They point at the owning
	// service's storage backend rather than its API, and must be requested
	// without its API credentials — S3 rejects a presigned request that also
	// carries an Authorization header.
	fetchClient *http.Client
}

// NewProxyService creates a ProxyService. It fails if cfg's service is not in
// the registry, so a misnamed service is caught at startup rather than on the
// first upload.
func NewProxyService(caller ServiceCaller, cfg ProxyConfig) (*ProxyService, error) {
	if caller == nil {
		return nil, fmt.Errorf("storage proxy: a service caller is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("storage proxy: %w", err)
	}
	if _, err := caller.GetClient(cfg.Service); err != nil {
		return nil, fmt.Errorf("storage proxy: %w", err)
	}
	return &ProxyService{
		caller:      caller,
		cfg:         cfg,
		fetchClient: &http.Client{Timeout: proxyFetchTimeout},
	}, nil
}

// Upload asks the owning service to allocate a key for the file and presign
// an upload URL for it, on the endpoint it serves users on.
func (s *ProxyService) Upload(ctx context.Context, filename string, size int64, mime string) (*corestorage.FileMetadata, error) {
	return s.reserve(ctx, s.cfg.UploadPath, filename, size, mime)
}

// reserve asks the owning service, on path, to allocate a key for the file and
// presign an upload URL for it.
func (s *ProxyService) reserve(ctx context.Context, path, filename string, size int64, mime string) (*corestorage.FileMetadata, error) {
	var resp struct {
		corestorage.FileMetadata
		Error string `json:"error"`
	}
	err := s.caller.Call(ctx, s.cfg.Service, remote.Request{
		Method: http.MethodPost,
		Path:   path,
		Body: remote.JSONBody{V: map[string]any{
			"filename":  filename,
			"mime_type": mime,
			"size":      size,
		}},
	}, &resp)
	if err != nil {
		return nil, remoteFailure("upload", err, resp.Error)
	}
	if resp.Key == "" || resp.UploadURL == "" {
		return nil, wrapUpstreamFailure("upload", errors.New("response missing key or upload_url"))
	}
	if err := requireAbsoluteURL(resp.UploadURL); err != nil {
		return nil, wrapUpstreamFailure("validate upload URL", err)
	}
	meta := resp.FileMetadata
	return &meta, nil
}

// Save stores content this deployment generated on the owning service: it
// allocates the key on the endpoint for such content, as Upload does for a
// user's file, then puts the content to the presigned URL itself. The owning
// service decides which MIME types that endpoint accepts.
func (s *ProxyService) Save(ctx context.Context, filename, mime string, body io.Reader, size int64) (*corestorage.FileMetadata, error) {
	meta, err := s.reserve(ctx, s.cfg.InternalUploadPath, filename, size, mime)
	if err != nil {
		return nil, err
	}
	// An owning service that omits the type from its reply signed the URL
	// for the one requested.
	if meta.MimeType == "" {
		meta.MimeType = mime
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, meta.UploadURL, body)
	if err != nil {
		return nil, fmt.Errorf("storage proxy: build upload request: %w", err)
	}
	// The URL was signed for this size, and a presigned S3 upload refuses a
	// chunked body; net/http sends one for a reader it can't measure.
	req.ContentLength = size
	// A presigned upload is signed over its content type, so it must be sent
	// as the type it was allocated for.
	req.Header.Set("Content-Type", meta.MimeType)
	resp, err := s.fetchClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage proxy: upload %s: %w", meta.Key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxProxyErrorBody))
		return nil, fmt.Errorf("storage proxy: upload %s: owning service returned %d: %s", meta.Key, resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	// The URL was for this upload only; the key is what to persist.
	meta.UploadURL = ""
	return meta, nil
}

// GetDownloadURL returns the owning service's time-limited download URL.
func (s *ProxyService) GetDownloadURL(ctx context.Context, key string) (string, error) {
	dl, err := s.DownloadURL(ctx, key)
	return dl.URL, err
}

// DownloadURL returns the owning service's time-limited download URL together
// with the Unix time it expires at, the type of the file and how the URL
// serves it, as the owning service reported them. An owning service that
// reports no disposition leaves it empty.
func (s *ProxyService) DownloadURL(ctx context.Context, key string) (corestorage.Download, error) {
	dl, err := s.fileDownload(ctx, key)
	return corestorage.Download{
		URL:         dl.DownloadURL,
		ExpiresAt:   dl.ExpiresAt,
		ContentType: dl.MimeType,
		Disposition: dl.Disposition,
	}, err
}

// fileDownload returns the owning service's download of the file, with the
// extension, type and disposition it gives for it, if any.
func (s *ProxyService) fileDownload(ctx context.Context, key string) (fileDownload, error) {
	var resp struct {
		fileDownload
		Error string `json:"error"`
	}
	err := s.caller.Call(ctx, s.cfg.Service, remote.Request{
		Method: http.MethodGet,
		Path:   keyPath(s.cfg.DownloadPath, key),
	}, &resp)
	if err != nil {
		return fileDownload{}, remoteFailure("get download URL", err, resp.Error)
	}
	if resp.DownloadURL == "" {
		return fileDownload{}, wrapUpstreamFailure("get download URL", errors.New("response missing download_url"))
	}
	if err := requireAbsoluteURL(resp.DownloadURL); err != nil {
		return fileDownload{}, wrapUpstreamFailure("validate download URL", err)
	}
	return resp.fileDownload, nil
}

// Download streams the file from the owning service via its download URL.
// Every failure is the owning service's (its URL, the route to it, or its
// answer), so each is an upstream failure.
func (s *ProxyService) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	downloadURL, err := s.GetDownloadURL(ctx, key)
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, "", wrapUpstreamFailure("build download request", err)
	}
	resp, err := s.fetchClient.Do(req)
	if err != nil {
		return nil, "", wrapUpstreamFailure("download "+key, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxProxyErrorBody))
		return nil, "", wrapUpstreamFailure("download "+key, fmt.Errorf("owning service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))))
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return resp.Body, contentType, nil
}

// Delete removes the file from the owning service.
func (s *ProxyService) Delete(ctx context.Context, key string) error {
	var resp struct {
		Error string `json:"error"`
	}
	err := s.caller.Call(ctx, s.cfg.Service, remote.Request{
		Method: http.MethodDelete,
		Path:   keyPath(s.cfg.DeletePath, key),
	}, &resp)
	if err != nil {
		return remoteFailure("delete", err, resp.Error)
	}
	return nil
}

// RemoteRejection is a client error (4xx) the owning service returned for a
// request, other than a rejection of this service's own credentials. It is
// the caller's request that was refused — an unsupported file type, say — so
// ProxyHandler relays it rather than reporting a gateway failure.
type RemoteRejection struct {
	StatusCode int
	// Message is the owning service's error message, when it sent one.
	Message string
	err     error
}

// upstreamFailure marks an error caused by the owning service or the route to
// it. HTTP handlers answer it as a gateway failure; a RemoteRejection wraps
// one when the owner returned a client error that should instead be relayed.
type upstreamFailure struct {
	err error
}

func (e *upstreamFailure) Error() string { return e.err.Error() }

func (e *upstreamFailure) Unwrap() error { return e.err }

func (e *RemoteRejection) Error() string {
	return e.err.Error()
}

func (e *RemoteRejection) Unwrap() error {
	return e.err
}

// remoteFailure wraps an error from the owning service's API, classifying a
// 4xx rejection of the request as a *RemoteRejection. remote reads the error
// body into the response before failing, which is where message comes from.
func remoteFailure(op string, err error, message string) error {
	wrapped := wrapUpstreamFailure(op, err)

	var re *remote.RemoteError
	if !errors.As(err, &re) || re.StatusCode < 400 || re.StatusCode > 499 {
		return wrapped
	}
	// 401/403 mean the owning service refused this service's credentials —
	// a misconfiguration here, not a problem with the caller's request.
	if re.StatusCode == http.StatusUnauthorized || re.StatusCode == http.StatusForbidden {
		return wrapped
	}
	return &RemoteRejection{StatusCode: re.StatusCode, Message: message, err: wrapped}
}

func wrapUpstreamFailure(op string, err error) *upstreamFailure {
	return &upstreamFailure{err: fmt.Errorf("storage proxy: %s: %w", op, err)}
}

// keyPath substitutes the escaped key for every {key} in a path template.
func keyPath(template, key string) string {
	return strings.ReplaceAll(template, KeyPlaceholder, url.PathEscape(key))
}

// requireAbsoluteURL rejects a relative URL from the owning service: handed to
// a client, it would resolve against this service instead of the owner.
func requireAbsoluteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("storage proxy: owning service returned non-absolute URL %q; its storage public URL must be absolute", raw)
	}
	return nil
}
