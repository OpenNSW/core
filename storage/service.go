// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"time"

	"github.com/OpenNSW/core/shared/audit"
	"github.com/OpenNSW/core/storage/drivers"
	"github.com/google/uuid"
)

// ErrInvalidKey is returned when a key is not one Upload could have created.
// Callers can use errors.Is(err, storage.ErrInvalidKey) to detect it.
var ErrInvalidKey = errors.New("storage: invalid key")

// ErrNotFound is returned by GetDownloadURL and DownloadURL for a file the
// backend does not hold. Callers can use errors.Is(err, storage.ErrNotFound)
// to detect it.
var ErrNotFound = drivers.ErrNotFound

// storageKeyRx matches a UUID, optionally followed by a dot and an alphanumeric extension.
var storageKeyRx = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(\.[a-zA-Z0-9]+)?$`)

// validStorageKey returns true if key matches UUID or UUID plus extension (e.g. .pdf).
func validStorageKey(key string) bool {
	return len(key) >= 36 && storageKeyRx.MatchString(key)
}

var (
	// ErrInvalidSize is returned by Upload for a size that is not positive.
	ErrInvalidSize = errors.New("storage: size must be greater than 0")
	// ErrContentTypeNotAllowed is returned by Upload for a MIME type outside
	// the list set with WithAllowedUploadTypes.
	ErrContentTypeNotAllowed = errors.New("storage: content type not allowed")
)

// FileTooLargeError is returned by Upload for a file larger than the limit.
type FileTooLargeError struct {
	Limit int64
}

func (e *FileTooLargeError) Error() string {
	return fmt.Sprintf("storage: file size exceeds %s limit", formatSize(e.Limit))
}

// defaultMaxUploadSize is the largest file Upload accepts unless
// WithMaxUploadSize sets another limit.
const defaultMaxUploadSize int64 = 32 << 20

// Service coordinates file storage operations and manages metadata
type Service struct {
	Driver StorageDriver

	// allowedUploadTypes limits Upload to these MIME types; nil allows any.
	allowedUploadTypes map[string]struct{}
	// maxUploadSize is the largest file Upload accepts; 0 means
	// defaultMaxUploadSize.
	maxUploadSize int64
	// inline is the set of types a download opens in the browser with. It is
	// nil unless WithInlineTypes set it, and then the default list applies.
	inline inlinePolicy

	Auditor audit.Auditor // optional; nil means no auditing
}

// ServiceOption configures a Service.
type ServiceOption func(*Service)

// WithAllowedUploadTypes limits Upload to the given MIME types; any other
// type is rejected with ErrContentTypeNotAllowed. Called with no types, it
// rejects every upload. Without it, Upload accepts any type. It does not
// apply to Save.
func WithAllowedUploadTypes(types ...string) ServiceOption {
	return func(s *Service) {
		s.allowedUploadTypes = make(map[string]struct{}, len(types))
		for _, t := range types {
			s.allowedUploadTypes[t] = struct{}{}
		}
	}
}

// WithMaxUploadSize sets the largest file, in bytes, that Upload accepts.
// Without it the limit is 32MB. It panics if n is not positive. It does not
// apply to Save.
func WithMaxUploadSize(n int64) ServiceOption {
	if n <= 0 {
		panic(fmt.Sprintf("storage: WithMaxUploadSize: size must be positive, got %d", n))
	}
	return func(s *Service) {
		s.maxUploadSize = n
	}
}

// WithInlineTypes sets the media types a download opens in the browser with;
// every other file is saved. Without it the types are DefaultInlineTypes.
// Called with no types, it saves every file. It panics if a type is not one
// ValidMediaType accepts.
//
// List a type a browser interprets as a document, such as text/html, only when
// the service stores that type itself, for example documents it generates with
// Save, and clients cannot upload it: NewService panics when a type that
// WithAllowedUploadTypes lets clients upload (or any type, without that
// option) also opens inline and is not one of DefaultInlineTypes.
func WithInlineTypes(types ...string) ServiceOption {
	for _, t := range types {
		if err := ValidMediaType(t); err != nil {
			panic(fmt.Sprintf("storage: WithInlineTypes: %v", err))
		}
	}
	return func(s *Service) {
		s.inline = newInlinePolicy(types)
	}
}

// NewService returns a Service over driver. It panics when an option is
// invalid, and when clients may upload a type the Service opens in the
// browser although it is not one of DefaultInlineTypes (see WithInlineTypes).
func NewService(driver StorageDriver, opts ...ServiceOption) *Service {
	s := &Service{Driver: driver}
	for _, opt := range opts {
		opt(s)
	}
	if err := s.checkUploadableInline(); err != nil {
		panic(err.Error())
	}
	return s
}

// checkUploadableInline reports a type clients may upload that the Service
// opens in the browser without it being one of DefaultInlineTypes.
func (s *Service) checkUploadableInline() error {
	if s.inline == nil {
		return nil // the default list names only inert types
	}
	uploadable := make([]string, 0, len(s.allowedUploadTypes))
	if s.allowedUploadTypes == nil {
		// Any type may be uploaded, so every inline type can be.
		for t := range s.inline {
			uploadable = append(uploadable, t)
		}
	} else {
		for t := range s.allowedUploadTypes {
			uploadable = append(uploadable, t)
		}
	}
	if unsafe := s.inline.notInert(uploadable); len(unsafe) > 0 {
		return errUploadableInline(unsafe)
	}
	return nil
}

// inlineTypes returns the policy a download is served under.
func (s *Service) inlineTypes() inlinePolicy {
	if s.inline == nil {
		return defaultInline
	}
	return s.inline
}

func (s *Service) uploadLimit() int64 {
	if s.maxUploadSize == 0 {
		return defaultMaxUploadSize
	}
	return s.maxUploadSize
}

// formatSize renders n in whole megabytes when it is a multiple of one, so
// the default reads "32MB", and in bytes otherwise.
func formatSize(n int64) string {
	if n%(1<<20) == 0 {
		return fmt.Sprintf("%dMB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// WithAuditor injects an optional auditor for recording storage audit events.
// Passing nil disables auditing.
func (s *Service) WithAuditor(auditor audit.Auditor) {
	s.Auditor = auditor
}

// Upload handles the preparation of a file upload by generating a unique key
// and a presigned/upload URL via the storage driver. The size and type are
// checked against the Service's limits here, because the presigned URL is
// what binds the upload to them.
func (s *Service) Upload(ctx context.Context, filename string, size int64, mime string) (*FileMetadata, error) {
	if size <= 0 {
		return nil, ErrInvalidSize
	}
	if limit := s.uploadLimit(); size > limit {
		return nil, &FileTooLargeError{Limit: limit}
	}
	if mime == "" {
		mime = drivers.DefaultMime
	}
	if s.allowedUploadTypes != nil {
		if _, ok := s.allowedUploadTypes[mime]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrContentTypeNotAllowed, mime)
		}
	}
	id, key := newKey(filename)

	// Generate a presigned URL for the upload
	uploadURL, err := s.Driver.GetUploadURL(ctx, key, mime, size)
	if err != nil {
		s.audit(ctx, eventTypePresignUpload, audit.ActionCreate, audit.StatusFailure, AuditDetails{
			Key: key, Filename: filename, MimeType: mime, Size: size, Error: err.Error(),
		})
		return nil, fmt.Errorf("failed to generate upload URL: %w", err)
	}

	metadata := &FileMetadata{
		ID:        id,
		Name:      filename,
		Key:       key,
		UploadURL: uploadURL,
		Size:      size,
		MimeType:  mime,
	}

	s.audit(ctx, eventTypePresignUpload, audit.ActionCreate, audit.StatusSuccess, AuditDetails{
		Key: key, Filename: filename, MimeType: mime, Size: size,
	})

	return metadata, nil
}

// Save stores content the caller already holds under a new key and returns
// its metadata. Persist the returned Key: Save stores only the bytes and the
// content type, so the key is the only way to find the file again.
//
// Unlike Upload, there is no URL for anyone to upload to, and the Service's
// upload limits (WithAllowedUploadTypes, WithMaxUploadSize) don't apply: they
// govern what clients may upload, and a service decides for itself what it
// stores.
//
// size is recorded in the returned metadata as given; Save doesn't count or
// check it. body is passed to the driver as is. For S3 over plain HTTP (e.g.
// a local MinIO), pass a seekable reader such as *bytes.Reader or *os.File.
func (s *Service) Save(ctx context.Context, filename, mime string, body io.Reader, size int64) (*FileMetadata, error) {
	if mime == "" {
		mime = drivers.DefaultMime
	}
	id, key := newKey(filename)

	if err := s.Driver.Save(ctx, key, body, mime); err != nil {
		return nil, fmt.Errorf("failed to save file: %w", err)
	}

	return &FileMetadata{
		ID:       id,
		Name:     filename,
		Key:      key,
		Size:     size,
		MimeType: mime,
	}, nil
}

// newKey creates the ID and storage key for a file named filename, for both
// Upload and Save.
func newKey(filename string) (id, key string) {
	id = uuid.NewString()
	key = id + filepath.Ext(filename)
	// Keep the filename's extension only when the result is a key the
	// download and delete routes accept; otherwise the file could be stored
	// but never fetched or removed again.
	if !validStorageKey(key) {
		key = id
	}
	return id, key
}

// checkKey rejects a key Upload could not have created, so a caller cannot
// reach an object in the backend by any other name.
func checkKey(key string) error {
	if !validStorageKey(key) {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	return nil
}

// Download retrieves the file content and its MIME type
func (s *Service) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	if err := checkKey(key); err != nil {
		return nil, "", err
	}
	rc, mime, err := s.Driver.Get(ctx, key)
	if err != nil {
		s.audit(ctx, eventTypeStorage, audit.ActionRead, audit.StatusFailure, AuditDetails{
			Key: key, Error: err.Error(),
		})
		return nil, "", err
	}
	s.audit(ctx, eventTypeStorage, audit.ActionRead, audit.StatusSuccess, AuditDetails{Key: key, MimeType: mime})
	return rc, mime, nil
}

// Download is a time-limited URL for a stored file, with what the file is and
// how the URL serves it.
type Download struct {
	URL string
	// ExpiresAt is the Unix time, in seconds, at which URL stops working.
	ExpiresAt int64
	// ContentType is the type the file was stored with.
	ContentType string
	// Disposition is drivers.DispositionInline when a browser opens the file,
	// and drivers.DispositionAttachment when it saves it.
	Disposition string
}

// DownloadURL generates a time-limited or presigned URL for the given key.
// The Service decides how the file is served from the type it was stored
// with: it opens in the browser only when WithInlineTypes lists the type, and
// is saved otherwise. A file the backend does not hold is an error wrapping
// ErrNotFound.
func (s *Service) DownloadURL(ctx context.Context, key string) (Download, error) {
	if err := checkKey(key); err != nil {
		return Download{}, err
	}
	// Taken before the URL is signed so the reported expiry is never later
	// than the real one.
	expiresAt := time.Now().Add(s.presignTTL()).Unix()

	info, err := s.Driver.Stat(ctx, key)
	if err != nil {
		s.audit(ctx, eventTypeStorage, audit.ActionRead, audit.StatusFailure, AuditDetails{
			Key: key, Error: err.Error(),
		})
		return Download{}, err
	}
	disposition := s.inlineTypes().disposition(info.ContentType)
	url, err := s.Driver.GetDownloadURL(ctx, key, disposition)
	if err != nil {
		s.audit(ctx, eventTypeStorage, audit.ActionRead, audit.StatusFailure, AuditDetails{
			Key: key, Error: err.Error(),
		})
		return Download{}, err
	}
	s.audit(ctx, eventTypeStorage, audit.ActionRead, audit.StatusSuccess, AuditDetails{Key: key})
	return Download{URL: url, ExpiresAt: expiresAt, ContentType: info.ContentType, Disposition: disposition}, nil
}

// GetDownloadURL is DownloadURL without the rest of the Download.
func (s *Service) GetDownloadURL(ctx context.Context, key string) (string, error) {
	d, err := s.DownloadURL(ctx, key)
	return d.URL, err
}

// presignTTL reports how long the driver's presigned URLs stay valid. A
// driver that does not say is assumed to use drivers.DefaultPresignTTL.
func (s *Service) presignTTL() time.Duration {
	if d, ok := s.Driver.(interface{ PresignTTL() time.Duration }); ok {
		return d.PresignTTL()
	}
	return drivers.DefaultPresignTTL
}

// Delete removes a file from storage
func (s *Service) Delete(ctx context.Context, key string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	err := s.Driver.Delete(ctx, key)
	if err != nil {
		s.audit(ctx, eventTypeStorage, audit.ActionDelete, audit.StatusFailure, AuditDetails{
			Key: key, Error: err.Error(),
		})
		return fmt.Errorf("failed to delete file: %w", err)
	}
	s.audit(ctx, eventTypeStorage, audit.ActionDelete, audit.StatusSuccess, AuditDetails{Key: key})
	return nil
}

const (
	eventTypeStorage       = "STORAGE"
	eventTypePresignUpload = "PRESIGN_UPLOAD"
)

// audit is a nil-safe helper that emits an event only when an Auditor is set.
func (s *Service) audit(ctx context.Context, eventType string, action audit.Action, status audit.Status, d AuditDetails) {
	if s.Auditor == nil {
		return
	}
	s.Auditor.Audit(ctx, audit.Event{
		Timestamp:  time.Now().UTC(),
		EventType:  eventType,
		Action:     action,
		Status:     status,
		TargetType: "RESOURCE",
		TargetID:   d.Key,
		Details:    d,
	})
}
