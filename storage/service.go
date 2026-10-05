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

// Service coordinates file storage operations and manages metadata
type Service struct {
	Driver StorageDriver
}

func NewService(driver StorageDriver) *Service {
	return &Service{Driver: driver}
}

// Upload handles the preparation of a file upload by generating a unique key
// and a presigned/upload URL via the storage driver.
func (s *Service) Upload(ctx context.Context, filename string, size int64, mime string) (*FileMetadata, error) {
	if mime == "" {
		mime = drivers.DefaultMime
	}
	id := uuid.NewString()
	key := id + filepath.Ext(filename)
	// Keep the filename's extension only when the result is a key the
	// download and delete routes accept; otherwise the file could be uploaded
	// but never fetched or removed again.
	if !validStorageKey(key) {
		key = id
	}

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
