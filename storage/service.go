package storage

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/OpenNSW/nsw/backend/pkg/storage/drivers"
	"github.com/google/uuid"
)

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
	ext := filepath.Ext(filename)
	key := fmt.Sprintf("%s%s", id, ext)

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

	slog.InfoContext(ctx, "File upload prepared", "id", id, "key", key)
	return metadata, nil
}

// Download retrieves the file content and its MIME type
func (s *Service) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	return s.Driver.Get(ctx, key)
}

// GetDownloadURL generates a time-limited or presigned URL for the given key
func (s *Service) GetDownloadURL(ctx context.Context, key string) (string, error) {
	return s.Driver.GetDownloadURL(ctx, key)
}

// Delete removes a file from storage
func (s *Service) Delete(ctx context.Context, key string) error {
	err := s.Driver.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	slog.InfoContext(ctx, "File deleted successfully", "key", key)
	return nil
}
