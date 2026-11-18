// Package storage selects the file storage this deployment serves uploads
// from: either a storage backend of its own (core/storage — local disk or S3)
// or a proxy onto another service that owns the files.
//
// Everything outside this package depends on the Service and Handler
// interfaces rather than on core/storage directly, so the choice is made once,
// here, from storage.type.
package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"

	corestorage "github.com/OpenNSW/core/storage"
	"github.com/OpenNSW/core/storage/drivers"
)

// Service is the file storage the rest of the application uses. It is
// satisfied by core/storage's *Service and by ProxyService.
type Service interface {
	// Upload reserves a key for a file and returns its metadata, including
	// the URL the client uploads the file to.
	Upload(ctx context.Context, filename string, size int64, mime string) (*corestorage.FileMetadata, error)
	// Download streams a stored file and returns its MIME type.
	Download(ctx context.Context, key string) (io.ReadCloser, string, error)
	// GetDownloadURL returns a time-limited URL the client downloads the file from.
	GetDownloadURL(ctx context.Context, key string) (string, error)
	// DownloadURL is GetDownloadURL plus the Unix time the URL expires at, the
	// type the file was stored with and how the URL serves it: inline when a
	// browser opens the file, attachment when it saves it.
	DownloadURL(ctx context.Context, key string) (corestorage.Download, error)
	// Delete removes a stored file.
	Delete(ctx context.Context, key string) error
	// Save stores content this service produced itself — a generated
	// document, say — under a new key and returns its metadata. Unlike
	// Upload, nothing is left for a client to do, and the upload limits don't
	// apply. size is the length of body; it is recorded as given.
	Save(ctx context.Context, filename, mime string, body io.Reader, size int64) (*corestorage.FileMetadata, error)
}

// Handler serves the storage API routes. It is satisfied by core/storage's
// *HTTPHandler and by ProxyHandler.
type Handler interface {
	Upload(w http.ResponseWriter, r *http.Request)
	Download(w http.ResponseWriter, r *http.Request)
	Delete(w http.ResponseWriter, r *http.Request)
}

// Stack is the storage service and HTTP handlers storage.type selected.
type Stack struct {
	Service Service
	Handler Handler
	// LocalContent serves the upload/download content routes that stand in
	// for S3 when this deployment stores files on local disk. Nil otherwise:
	// with S3 the client talks to the bucket, and behind a proxy it talks to
	// the owning service.
	LocalContent *corestorage.LocalContentHandler
	// Uploads prepares the uploads users make: Service itself, or in proxy
	// mode Service limited to the types in storage.proxy.allowedUploadTypes.
	Uploads Uploader
	// MachineUploads prepares the uploads machine clients make on the
	// internal upload route. Nil in proxy mode, where nothing uploads to this
	// deployment.
	MachineUploads Uploader
}

// Option adjusts the stack New builds.
type Option func(*options)

type options struct {
	internalUploadTypes map[string][]string
}

// WithInternalUploadTypes sets the media types, beyond storage.allowedUploadTypes,
// each machine client may upload on the internal upload route, by client ID.
func WithInternalUploadTypes(byClientID map[string][]string) Option {
	return func(o *options) { o.internalUploadTypes = byClientID }
}

// New builds the storage stack for cfg.Type: a proxy onto another service
// when it is TypeProxy, otherwise the core/storage backend it names. caller
// reaches the owning service and is only used in proxy mode.
func New(ctx context.Context, cfg Config, caller ServiceCaller, opts ...Option) (*Stack, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	if cfg.IsProxy() {
		svc, err := NewProxyService(caller, cfg.Proxy)
		if err != nil {
			return nil, err
		}
		return &Stack{
			Service: svc,
			Handler: NewProxyHandler(svc),
			Uploads: limitTypes(svc, anyCaller(cfg.Proxy.AllowedUploadTypes)),
		}, nil
	}

	if err := cfg.validateBackend(); err != nil {
		return nil, err
	}
	// The local content routes sit beside the rest of the storage API, not
	// wherever core/storage's default puts them. validateBackend allows only
	// an unset or matching value here, so nothing configured is overridden.
	cfg.Local.RoutePrefix = RoutePrefix
	driver, err := corestorage.NewStorageFromConfig(ctx, cfg.Config)
	if err != nil {
		return nil, fmt.Errorf("storage backend: %w", err)
	}
	svc := corestorage.NewService(driver,
		corestorage.WithAllowedUploadTypes(cfg.AllowedUploadTypes...),
		corestorage.WithInlineTypes(cfg.InlineTypes...),
		corestorage.WithMaxUploadSize(cfg.MaxUploadBytes),
		corestorage.WithUploadTypeExtensions(cfg.UploadTypeExtensions),
	)

	stack := &Stack{
		Service:        svc,
		Handler:        corestorage.NewHTTPHandler(svc),
		Uploads:        svc,
		MachineUploads: newMachineUploads(driver, cfg, o.internalUploadTypes),
	}
	if local, ok := driver.(*drivers.LocalFSDriver); ok {
		stack.LocalContent = corestorage.NewLocalContentHandler(local)
	}
	return stack, nil
}
