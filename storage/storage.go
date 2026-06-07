package storage

import (
	"context"
	"io"
)

// StorageDriver defines how we interact with the binary storage
type StorageDriver interface {
	// Save writes the content to the storage and returns a unique identifier (key/path)
	Save(ctx context.Context, key string, body io.Reader, contentType string) error

	// Get returns a ReadCloser to stream the file back and its content type
	Get(ctx context.Context, key string) (io.ReadCloser, string, error)

	// Delete removes the file
	Delete(ctx context.Context, key string) error

	// GetDownloadURL returns a presigned or time-limited URL for downloading
	GetDownloadURL(ctx context.Context, key string) (string, error)

	// GetUploadURL returns a presigned URL for uploading a file directly to storage
	GetUploadURL(ctx context.Context, key string, contentType string, maxSizeBytes int64) (string, error)
}
