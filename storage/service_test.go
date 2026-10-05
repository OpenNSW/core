// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/OpenNSW/core/storage/drivers"
)

// testKey is a key Upload could have created.
const testKey = "550e8400-e29b-41d4-a716-446655440000.pdf"

// MockDriver implements StorageDriver for testing
type MockDriver struct {
	SavedKey       string
	SavedBody      []byte
	GenerateURLErr error
	DeleteCalled   bool
	DeleteKey      string
}

func (m *MockDriver) Save(ctx context.Context, key string, body io.Reader, contentType string) error {
	m.SavedKey = key
	content, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.SavedBody = content
	return nil
}

func (m *MockDriver) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	return io.NopCloser(bytes.NewReader(m.SavedBody)), "application/test", nil
}

func (m *MockDriver) Delete(ctx context.Context, key string) error {
	m.DeleteCalled = true
	m.DeleteKey = key
	return nil
}

func (m *MockDriver) GetDownloadURL(ctx context.Context, key string) (string, error) {
	if m.GenerateURLErr != nil {
		return "", m.GenerateURLErr
	}
	return "/test/download/" + key, nil
}

func (m *MockDriver) GetUploadURL(ctx context.Context, key string, contentType string, maxSizeBytes int64) (string, error) {
	if m.GenerateURLErr != nil {
		return "", m.GenerateURLErr
	}
	return "/test/upload/" + key, nil
}

func TestUploadService(t *testing.T) {
	mock := &MockDriver{}
	service := NewService(mock)

	ctx := context.Background()
	filename := "test.jpg"
	size := int64(1024)

	metadata, err := service.Upload(ctx, filename, size, "image/jpeg")
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	if metadata.Name != filename {
		t.Errorf("expected name %s, got %s", filename, metadata.Name)
	}

	if metadata.Size != size {
		t.Errorf("expected size %d, got %d", size, metadata.Size)
	}

	if metadata.UploadURL != "/test/upload/"+metadata.Key {
		t.Errorf("unexpected upload URL: %s", metadata.UploadURL)
	}
}

func TestUploadService_KeyExtension(t *testing.T) {
	tests := []struct {
		filename string
		wantExt  string
	}{
		{filename: "report.pdf", wantExt: ".pdf"},
		{filename: "archive.tar.gz", wantExt: ".gz"},
		{filename: "report.final-v2", wantExt: ""},
		{filename: "x.p df", wantExt: ""},
		{filename: "README", wantExt: ""},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			service := NewService(&MockDriver{})

			metadata, err := service.Upload(context.Background(), tt.filename, 1024, "application/pdf")
			if err != nil {
				t.Fatalf("Upload failed: %v", err)
			}

			if want := metadata.ID + tt.wantExt; metadata.Key != want {
				t.Errorf("expected key %s, got %s", want, metadata.Key)
			}
			if !validStorageKey(metadata.Key) {
				t.Errorf("key %s is rejected by validStorageKey", metadata.Key)
			}
		})
	}
}

func TestUploadService_Download(t *testing.T) {
	mock := &MockDriver{
		SavedBody: []byte("test content"),
	}
	service := NewService(mock)

	ctx := context.Background()
	reader, contentType, err := service.Download(ctx, testKey)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	defer reader.Close()

	if contentType != "application/test" {
		t.Errorf("expected content type application/test, got %s", contentType)
	}

	content, _ := io.ReadAll(reader)
	if !bytes.Equal(content, mock.SavedBody) {
		t.Error("downloaded content does not match saved body")
	}
}

func TestUploadService_GetDownloadURL_Success(t *testing.T) {
	mock := &MockDriver{}
	service := NewService(mock)

	ctx := context.Background()
	const key = testKey

	url, err := service.GetDownloadURL(ctx, key)
	if err != nil {
		t.Fatalf("GetDownloadURL failed: %v", err)
	}

	if url != "/test/download/"+key {
		t.Errorf("unexpected URL: %s", url)
	}
}

func TestUploadService_GetDownloadURL_Error(t *testing.T) {
	expectedErr := io.ErrUnexpectedEOF
	mock := &MockDriver{GenerateURLErr: expectedErr}
	service := NewService(mock)

	_, err := service.GetDownloadURL(context.Background(), testKey)
	if err == nil {
		t.Fatal("expected error from GetDownloadURL, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestUploadService_RejectsInvalidKeys(t *testing.T) {
	keys := []string{"", "test-key", "../etc/passwd", "550e8400-e29b-41d4-a716-446655440000/other", "550e8400-e29b-41d4-a716-446655440000.final-v2"}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			mock := &MockDriver{}
			service := NewService(mock)
			ctx := context.Background()

			if _, _, err := service.Download(ctx, key); !errors.Is(err, ErrInvalidKey) {
				t.Errorf("Download: expected ErrInvalidKey, got %v", err)
			}
			if _, err := service.GetDownloadURL(ctx, key); !errors.Is(err, ErrInvalidKey) {
				t.Errorf("GetDownloadURL: expected ErrInvalidKey, got %v", err)
			}
			if err := service.Delete(ctx, key); !errors.Is(err, ErrInvalidKey) {
				t.Errorf("Delete: expected ErrInvalidKey, got %v", err)
			}
			if mock.DeleteCalled {
				t.Error("Delete reached the driver with an invalid key")
			}
		})
	}
}

// ttlDriver is a MockDriver that reports how long its presigned URLs last.
type ttlDriver struct {
	MockDriver
	ttl time.Duration
}

func (d *ttlDriver) PresignTTL() time.Duration { return d.ttl }

func TestUploadService_DownloadURL_ExpiresAt(t *testing.T) {
	tests := []struct {
		name   string
		driver StorageDriver
		ttl    time.Duration
	}{
		{name: "driver reports its TTL", driver: &ttlDriver{ttl: 5 * time.Minute}, ttl: 5 * time.Minute},
		{name: "driver without a TTL falls back to the default", driver: &MockDriver{}, ttl: drivers.DefaultPresignTTL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			url, expiresAt, err := NewService(tt.driver).DownloadURL(context.Background(), testKey)
			if err != nil {
				t.Fatalf("DownloadURL failed: %v", err)
			}
			if url != "/test/download/"+testKey {
				t.Errorf("unexpected URL: %s", url)
			}

			want := before.Add(tt.ttl).Unix()
			if expiresAt < want || expiresAt > want+2 {
				t.Errorf("expiresAt = %d, want about %d (now + %v)", expiresAt, want, tt.ttl)
			}
		})
	}
}

func TestUploadService_DownloadURL_InvalidKey(t *testing.T) {
	if _, _, err := NewService(&MockDriver{}).DownloadURL(context.Background(), "test-key"); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("expected ErrInvalidKey, got %v", err)
	}
}

func TestUploadService_Policy(t *testing.T) {
	pdfOnly := []ServiceOption{WithAllowedContentTypes("application/pdf")}

	tests := []struct {
		name     string
		opts     []ServiceOption
		mimeType string
		size     int64
		wantErr  error
	}{
		{name: "any type by default: xlsx", mimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", size: 1024},
		{name: "any type by default: legacy xls", mimeType: "application/vnd.ms-excel", size: 1024},
		{name: "any type by default: executable", mimeType: "application/x-msdownload", size: 1024},
		{name: "allowlisted type accepted", opts: pdfOnly, mimeType: "application/pdf", size: 1024},
		{name: "type outside allowlist rejected", opts: pdfOnly, mimeType: "application/x-msdownload", size: 1024, wantErr: ErrContentTypeNotAllowed},
		{name: "missing type checked as octet-stream", opts: pdfOnly, mimeType: "", size: 1024, wantErr: ErrContentTypeNotAllowed},
		{name: "empty allowlist rejects everything", opts: []ServiceOption{WithAllowedContentTypes()}, mimeType: "application/pdf", size: 1024, wantErr: ErrContentTypeNotAllowed},
		{name: "zero size rejected", mimeType: "application/pdf", size: 0, wantErr: ErrInvalidSize},
		{name: "negative size rejected", mimeType: "application/pdf", size: -1, wantErr: ErrInvalidSize},
		{name: "size checked before type", opts: pdfOnly, mimeType: "application/x-msdownload", size: 32<<20 + 1, wantErr: &FileTooLargeError{}},
		{name: "default limit: 32MB accepted", mimeType: "application/pdf", size: 32 << 20},
		{name: "default limit: one byte over rejected", mimeType: "application/pdf", size: 32<<20 + 1, wantErr: &FileTooLargeError{}},
		{name: "custom limit: one byte over rejected", opts: []ServiceOption{WithMaxUploadSize(1 << 20)}, mimeType: "application/pdf", size: 1<<20 + 1, wantErr: &FileTooLargeError{}},
		{name: "custom limit: above the default accepted", opts: []ServiceOption{WithMaxUploadSize(64 << 20)}, mimeType: "application/pdf", size: 33 << 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewService(&MockDriver{}, tt.opts...).Upload(context.Background(), "upload.bin", tt.size, tt.mimeType)

			switch want := tt.wantErr.(type) {
			case nil:
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
			case *FileTooLargeError:
				var tooLarge *FileTooLargeError
				if !errors.As(err, &tooLarge) {
					t.Fatalf("expected *FileTooLargeError, got %v", err)
				}
			default:
				if !errors.Is(err, want) {
					t.Fatalf("expected %v, got %v", want, err)
				}
			}
		})
	}
}

func TestFileTooLargeError_Message(t *testing.T) {
	tests := []struct {
		limit int64
		want  string
	}{
		{limit: 32 << 20, want: "storage: file size exceeds 32MB limit"},
		{limit: 1000, want: "storage: file size exceeds 1000 bytes limit"},
	}
	for _, tt := range tests {
		if got := (&FileTooLargeError{Limit: tt.limit}).Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}

func TestWithMaxUploadSize_PanicsOnNonPositive(t *testing.T) {
	for _, n := range []int64{0, -1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("WithMaxUploadSize(%d) did not panic", n)
				}
			}()
			WithMaxUploadSize(n)
		}()
	}
}

func TestUploadService_StructLiteralUsesDefaults(t *testing.T) {
	// A Service built without NewService still gets the default limit rather
	// than rejecting every upload.
	service := &Service{Driver: &MockDriver{}}

	if _, err := service.Upload(context.Background(), "test.pdf", 1024, "application/pdf"); err != nil {
		t.Fatalf("Upload failed: %v", err)
	}
}
