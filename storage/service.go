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

	"github.com/OpenNSW/core/storage/drivers"
	"github.com/google/uuid"
)

// ErrInvalidKey is returned when a key is not one Upload could have created.
// Callers can use errors.Is(err, storage.ErrInvalidKey) to detect it.
var ErrInvalidKey = errors.New("storage: invalid key")

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

func NewService(driver StorageDriver, opts ...ServiceOption) *Service {
	s := &Service{Driver: driver}
	for _, opt := range opts {
		opt(s)
	}
	return s
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
	return s.Driver.Get(ctx, key)
}

// GetDownloadURL generates a time-limited or presigned URL for the given key
func (s *Service) GetDownloadURL(ctx context.Context, key string) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}
	return s.Driver.GetDownloadURL(ctx, key)
}

// DownloadURL is GetDownloadURL plus the time, in unix seconds, at which the
// URL stops working.
func (s *Service) DownloadURL(ctx context.Context, key string) (string, int64, error) {
	// Taken before the URL is signed so the reported expiry is never later
	// than the real one.
	expiresAt := time.Now().Add(s.presignTTL()).Unix()
	url, err := s.GetDownloadURL(ctx, key)
	if err != nil {
		return "", 0, err
	}
	return url, expiresAt, nil
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
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}
