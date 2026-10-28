// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OpenNSW/core/storage/drivers"
)

// testKey is a key Upload could have created.
const testKey = "550e8400-e29b-41d4-a716-446655440000.pdf"

// MockDriver implements StorageDriver for testing
type MockDriver struct {
	SavedKey         string
	SavedBody        []byte
	SavedContentType string
	SaveErr          error
	GenerateURLErr   error
	DeleteCalled     bool
	DeleteKey        string

	// StoredType is the type Stat reports; StatErr makes Stat fail.
	StoredType string
	StatErr    error
	// DownloadDisposition is the disposition GetDownloadURL was last asked for.
	DownloadDisposition string
	// UploadURLs counts the upload URLs GetUploadURL was asked for.
	UploadURLs int
}

func (m *MockDriver) Save(ctx context.Context, key string, body io.Reader, contentType string) error {
	if m.SaveErr != nil {
		return m.SaveErr
	}
	m.SavedKey = key
	m.SavedContentType = contentType
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

func (m *MockDriver) Stat(ctx context.Context, key string) (drivers.ObjectInfo, error) {
	if m.StatErr != nil {
		return drivers.ObjectInfo{}, m.StatErr
	}
	return drivers.ObjectInfo{ContentType: m.StoredType, Size: int64(len(m.SavedBody))}, nil
}

func (m *MockDriver) GetDownloadURL(ctx context.Context, key string, disposition string) (string, error) {
	if m.GenerateURLErr != nil {
		return "", m.GenerateURLErr
	}
	m.DownloadDisposition = disposition
	return "/test/download/" + key, nil
}

func (m *MockDriver) GetUploadURL(ctx context.Context, key string, contentType string, maxSizeBytes int64) (string, error) {
	m.UploadURLs++
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

	// Upload and Save must create keys the same way.
	create := map[string]func(*Service, string) (*FileMetadata, error){
		"Upload": func(s *Service, filename string) (*FileMetadata, error) {
			return s.Upload(context.Background(), filename, 1024, "application/pdf")
		},
		"Save": func(s *Service, filename string) (*FileMetadata, error) {
			return s.Save(context.Background(), filename, "application/pdf", bytes.NewReader([]byte("data")), 4)
		},
	}

	for method, fn := range create {
		for _, tt := range tests {
			t.Run(method+"/"+tt.filename, func(t *testing.T) {
				metadata, err := fn(NewService(&MockDriver{}), tt.filename)
				if err != nil {
					t.Fatalf("%s failed: %v", method, err)
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
			download, err := NewService(tt.driver).DownloadURL(context.Background(), testKey)
			if err != nil {
				t.Fatalf("DownloadURL failed: %v", err)
			}
			if download.URL != "/test/download/"+testKey {
				t.Errorf("unexpected URL: %s", download.URL)
			}

			want := before.Add(tt.ttl).Unix()
			if download.ExpiresAt < want || download.ExpiresAt > want+2 {
				t.Errorf("ExpiresAt = %d, want about %d (now + %v)", download.ExpiresAt, want, tt.ttl)
			}
		})
	}
}

func TestUploadService_DownloadURL_Size(t *testing.T) {
	download, err := NewService(&MockDriver{SavedBody: []byte("test content")}).DownloadURL(context.Background(), testKey)
	if err != nil {
		t.Fatalf("DownloadURL failed: %v", err)
	}
	if download.Size != int64(len("test content")) {
		t.Errorf("Size = %d, want %d", download.Size, len("test content"))
	}
}

func TestUploadService_DownloadURL_InvalidKey(t *testing.T) {
	if _, err := NewService(&MockDriver{}).DownloadURL(context.Background(), "test-key"); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("expected ErrInvalidKey, got %v", err)
	}
}

// The Service decides how a file is served from the type it was stored with,
// and tells the driver and the caller what it decided.
func TestUploadService_DownloadURL_DispositionFollowsStoredType(t *testing.T) {
	tests := []struct {
		name       string
		opts       []ServiceOption
		storedType string
		want       string
	}{
		{name: "pdf", storedType: "application/pdf", want: drivers.DispositionInline},
		{name: "image", storedType: "image/png", want: drivers.DispositionInline},
		{name: "text with a parameter", storedType: "text/plain; charset=utf-8", want: drivers.DispositionInline},
		{name: "xml", storedType: "text/xml", want: drivers.DispositionAttachment},
		{name: "svg", storedType: "image/svg+xml", want: drivers.DispositionAttachment},
		{name: "html", storedType: "text/html; charset=utf-8", want: drivers.DispositionAttachment},
		{name: "spreadsheet", storedType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", want: drivers.DispositionAttachment},
		{name: "no type", storedType: "", want: drivers.DispositionAttachment},
		{name: "html listed inline", opts: []ServiceOption{WithInlineTypes("text/html"), WithAllowedUploadTypes("application/pdf")}, storedType: "text/html; charset=utf-8", want: drivers.DispositionInline},
		{name: "pdf not listed", opts: []ServiceOption{WithInlineTypes("text/html"), WithAllowedUploadTypes("application/pdf")}, storedType: "application/pdf", want: drivers.DispositionAttachment},
		{name: "empty list saves every file", opts: []ServiceOption{WithInlineTypes()}, storedType: "application/pdf", want: drivers.DispositionAttachment},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &MockDriver{StoredType: tt.storedType}
			download, err := NewService(mock, tt.opts...).DownloadURL(context.Background(), testKey)
			if err != nil {
				t.Fatalf("DownloadURL: %v", err)
			}
			if download.Disposition != tt.want {
				t.Errorf("Disposition = %q, want %q", download.Disposition, tt.want)
			}
			if mock.DownloadDisposition != tt.want {
				t.Errorf("the driver was asked for %q, want %q", mock.DownloadDisposition, tt.want)
			}
			if download.ContentType != tt.storedType {
				t.Errorf("ContentType = %q, want %q", download.ContentType, tt.storedType)
			}
		})
	}
}

func TestUploadService_DownloadURL_StructLiteralUsesDefaultInlineTypes(t *testing.T) {
	service := &Service{Driver: &MockDriver{StoredType: "application/pdf"}}
	download, err := service.DownloadURL(context.Background(), testKey)
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	if download.Disposition != drivers.DispositionInline {
		t.Errorf("Disposition = %q, want %q", download.Disposition, drivers.DispositionInline)
	}
}

func TestUploadService_DownloadURL_MissingFile(t *testing.T) {
	mock := &MockDriver{StatErr: fmt.Errorf("%w: %s", drivers.ErrNotFound, testKey)}

	download, err := NewService(mock).DownloadURL(context.Background(), testKey)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("DownloadURL: err = %v, want ErrNotFound", err)
	}
	if download != (Download{}) {
		t.Errorf("DownloadURL = %+v, want the zero Download", download)
	}
	if mock.DownloadDisposition != "" {
		t.Error("a URL was requested from the driver for a file it does not hold")
	}
}

func TestUploadService_DownloadURL_DriverErrors(t *testing.T) {
	statErr := errors.New("stat failure")
	if _, err := NewService(&MockDriver{StatErr: statErr}).DownloadURL(context.Background(), testKey); !errors.Is(err, statErr) {
		t.Errorf("Stat failure: err = %v, want %v", err, statErr)
	}
	signErr := errors.New("presign failure")
	if _, err := NewService(&MockDriver{GenerateURLErr: signErr}).DownloadURL(context.Background(), testKey); !errors.Is(err, signErr) {
		t.Errorf("GetDownloadURL failure: err = %v, want %v", err, signErr)
	}
}

func TestWithInlineTypes_PanicsOnAMalformedType(t *testing.T) {
	for _, bad := range []string{"", "pdf", "TEXT/HTML", "text/html; charset=utf-8", "image/*", "*/*"} {
		t.Run(bad, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("WithInlineTypes(%q) did not panic", bad)
				}
			}()
			WithInlineTypes("application/pdf", bad)
		})
	}
}

// A type clients may upload must not also open in the browser unless it is
// on the default list, which names only inert types.
func TestNewService_RefusesAnUploadableInlineType(t *testing.T) {
	tests := []struct {
		name      string
		opts      []ServiceOption
		wantPanic string // text the panic must name; empty means no panic
	}{
		{name: "defaults", opts: nil},
		{name: "html inline, only pdf uploadable", opts: []ServiceOption{WithInlineTypes("text/html"), WithAllowedUploadTypes("application/pdf")}},
		{name: "html inline, no uploads allowed", opts: []ServiceOption{WithInlineTypes("text/html"), WithAllowedUploadTypes()}},
		{name: "default types inline, any upload", opts: []ServiceOption{WithInlineTypes("application/pdf", "image/png")}},
		{name: "empty inline list, any upload", opts: []ServiceOption{WithInlineTypes()}},
		{name: "html inline and uploadable", opts: []ServiceOption{WithInlineTypes("text/html"), WithAllowedUploadTypes("application/pdf", "text/html")}, wantPanic: "text/html"},
		{name: "option order does not matter", opts: []ServiceOption{WithAllowedUploadTypes("text/html"), WithInlineTypes("text/html")}, wantPanic: "text/html"},
		{name: "html inline, any type uploadable", opts: []ServiceOption{WithInlineTypes("text/html")}, wantPanic: "text/html"},
		{name: "case and parameters do not hide it", opts: []ServiceOption{WithInlineTypes("text/html"), WithAllowedUploadTypes("TEXT/HTML; charset=utf-8")}, wantPanic: "TEXT/HTML; charset=utf-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				switch {
				case tt.wantPanic == "" && r != nil:
					t.Errorf("NewService panicked: %v", r)
				case tt.wantPanic != "" && r == nil:
					t.Errorf("NewService did not panic, want a panic naming %q", tt.wantPanic)
				case tt.wantPanic != "" && !strings.Contains(fmt.Sprint(r), tt.wantPanic):
					t.Errorf("panic %q does not name %q", r, tt.wantPanic)
				}
			}()
			NewService(&MockDriver{}, tt.opts...)
		})
	}
}

func TestUploadService_Policy(t *testing.T) {
	pdfOnly := []ServiceOption{WithAllowedUploadTypes("application/pdf")}

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
		{name: "empty allowlist rejects everything", opts: []ServiceOption{WithAllowedUploadTypes()}, mimeType: "application/pdf", size: 1024, wantErr: ErrContentTypeNotAllowed},
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

// testExtensions lists more than one extension for a type, so the order and the
// first entry matter.
var testExtensions = map[string][]string{
	"application/pdf": {".pdf"},
	"image/jpeg":      {".jpg", ".jpeg"},
	"text/html":       {".html", ".htm"},
}

func TestUploadService_TypeExtensions(t *testing.T) {
	withExtensions := []ServiceOption{WithUploadTypeExtensions(testExtensions)}
	// An empty type is uploaded as drivers.DefaultMime, and its name is
	// matched to that.
	withOctetStream := []ServiceOption{WithUploadTypeExtensions(map[string][]string{drivers.DefaultMime: {".bin"}})}

	accepted := []struct {
		name, filename, mimeType, want string
		opts                           []ServiceOption
	}{
		{name: "the type's extension", filename: "report.pdf", mimeType: "application/pdf", want: "report.pdf"},
		{name: "a later extension of several", filename: "photo.jpeg", mimeType: "image/jpeg", want: "photo.jpeg"},
		{name: "the extension in capitals", filename: "PHOTO.JPG", mimeType: "image/jpeg", want: "PHOTO.JPG"},
		{name: "the type in capitals, with parameters", filename: "page.htm", mimeType: "Text/HTML; charset=utf-8", want: "page.htm"},
		{name: "no extension: the type's first", filename: "photo", mimeType: "image/jpeg", want: "photo.jpg"},
		{name: "a type with no entry is not checked", filename: "data.bin", mimeType: "application/zip", want: "data.bin"},
		{name: "no map: nothing is checked", filename: "setup.exe", mimeType: "application/pdf", want: "setup.exe", opts: []ServiceOption{}},
		{name: "a name that is only an extension", filename: ".pdf", mimeType: "application/pdf", want: ".pdf"},
		{name: "a dot only before the last slash: no extension", filename: "a.exe/b", mimeType: "application/pdf", want: "a.exe/b.pdf"},
		{name: "no type, no extension: the default type's", filename: "x", mimeType: "", want: "x.bin", opts: withOctetStream},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			opts := withExtensions
			if tt.opts != nil {
				opts = tt.opts
			}
			meta, err := NewService(&MockDriver{}, opts...).Upload(context.Background(), tt.filename, 1024, tt.mimeType)
			if err != nil {
				t.Fatalf("Upload: %v", err)
			}
			if meta.Name != tt.want || filepath.Ext(meta.Key) != filepath.Ext(tt.want) {
				t.Errorf("name %q, key %q, want both to end like %q", meta.Name, meta.Key, tt.want)
			}
		})
	}

	refused := []struct {
		name, filename, mimeType, wantExt, wantType string
		opts                                        []ServiceOption
	}{
		{name: "a program sent as a PDF", filename: "setup.exe", mimeType: "application/pdf", wantExt: ".exe", wantType: "application/pdf"},
		{name: "another type's extension", filename: "report.pdf", mimeType: "image/jpeg", wantExt: ".pdf", wantType: "image/jpeg"},
		{name: "a name ending in a dot", filename: "report.", mimeType: "application/pdf", wantExt: ".", wantType: "application/pdf"},
		{name: "only the last extension counts", filename: "inv.pdf.exe", mimeType: "application/pdf", wantExt: ".exe", wantType: "application/pdf"},
		{name: "no type: matched to the default type", filename: "x.exe", mimeType: "", wantExt: ".exe", wantType: drivers.DefaultMime, opts: withOctetStream},
		{name: "a long extension, cut", filename: "report." + strings.Repeat("x", 1000), mimeType: "application/pdf", wantExt: "." + strings.Repeat("x", 15) + "…", wantType: "application/pdf"},
		{name: "the type with parameters", filename: "page.pdf", mimeType: "text/html; charset=utf-8", wantExt: ".pdf", wantType: "text/html"},
		{name: "the type with a malformed parameter", filename: "setup.exe", mimeType: "application/pdf;x", wantExt: ".exe", wantType: "application/pdf"},
		{name: "with an allowlist that admits the type", filename: "setup.exe", mimeType: "application/pdf", wantExt: ".exe", wantType: "application/pdf",
			opts: []ServiceOption{WithAllowedUploadTypes("application/pdf"), WithUploadTypeExtensions(testExtensions)}},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			opts := withExtensions
			if tt.opts != nil {
				opts = tt.opts
			}
			driver := &MockDriver{}
			_, err := NewService(driver, opts...).Upload(context.Background(), tt.filename, 1024, tt.mimeType)
			var mismatch *ExtensionMismatchError
			if !errors.As(err, &mismatch) {
				t.Fatalf("expected *ExtensionMismatchError, got %v", err)
			}
			if mismatch.Extension != tt.wantExt || mismatch.MediaType != tt.wantType {
				t.Errorf("mismatch %+v, want extension %q and type %q", *mismatch, tt.wantExt, tt.wantType)
			}
			// Nothing is reserved for a refused name.
			if driver.UploadURLs != 0 {
				t.Errorf("a refused name was given %d upload URLs", driver.UploadURLs)
			}
		})
	}

	t.Run("the size is checked before the name", func(t *testing.T) {
		svc := NewService(&MockDriver{}, withExtensions...)
		var tooLarge *FileTooLargeError
		if _, err := svc.Upload(context.Background(), "setup.exe", 32<<20+1, "application/pdf"); !errors.As(err, &tooLarge) {
			t.Fatalf("expected *FileTooLargeError, got %v", err)
		}
	})

	t.Run("the type is checked before the name", func(t *testing.T) {
		svc := NewService(&MockDriver{}, WithAllowedUploadTypes("application/pdf"), WithUploadTypeExtensions(testExtensions))
		if _, err := svc.Upload(context.Background(), "page.pdf", 1024, "text/html"); !errors.Is(err, ErrContentTypeNotAllowed) {
			t.Fatalf("expected ErrContentTypeNotAllowed, got %v", err)
		}
	})

	t.Run("the map is copied", func(t *testing.T) {
		byType := map[string][]string{"application/pdf": {".pdf"}}
		svc := NewService(&MockDriver{}, WithUploadTypeExtensions(byType))
		byType["application/pdf"][0] = ".exe"
		byType["image/png"] = []string{".png"}

		if _, err := svc.Upload(context.Background(), "setup.exe", 1024, "application/pdf"); err == nil {
			t.Error("a change to the slice after NewService let .exe through as a PDF")
		}
		if _, err := svc.Upload(context.Background(), "photo.jpg", 1024, "image/png"); err != nil {
			t.Errorf("a type added after NewService is checked: %v", err)
		}
	})

	t.Run("Save keeps the name as given", func(t *testing.T) {
		svc := NewService(&MockDriver{}, withExtensions...)
		meta, err := svc.Save(context.Background(), "setup.exe", "application/pdf", strings.NewReader("x"), 1)
		if err != nil || meta.Name != "setup.exe" {
			t.Fatalf("Save: %+v, %v", meta, err)
		}
	})
}

func TestCheckUploadTypeExtensions(t *testing.T) {
	tests := []struct {
		name    string
		byType  map[string][]string
		wantErr bool
	}{
		{name: "none", byType: nil},
		{name: "several types and extensions", byType: testExtensions},
		{name: "a type with parameters", byType: map[string][]string{"text/html; charset=utf-8": {".html"}}, wantErr: true},
		{name: "a type in capitals", byType: map[string][]string{"Application/PDF": {".pdf"}}, wantErr: true},
		{name: "a wildcard", byType: map[string][]string{"image/*": {".png"}}, wantErr: true},
		{name: "no extensions", byType: map[string][]string{"application/pdf": {}}, wantErr: true},
		{name: "an extension without its dot", byType: map[string][]string{"application/pdf": {"pdf"}}, wantErr: true},
		{name: "an extension in capitals", byType: map[string][]string{"application/pdf": {".PDF"}}, wantErr: true},
		{name: "an extension a key can't end in", byType: map[string][]string{"application/gzip": {".tar.gz"}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckUploadTypeExtensions(tt.byType)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckUploadTypeExtensions() = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantErr {
				defer func() {
					if recover() == nil {
						t.Error("WithUploadTypeExtensions did not panic")
					}
				}()
				WithUploadTypeExtensions(tt.byType)
			}
		})
	}
}

// With several mistakes, the first type in sorted order is reported, every time.
func TestCheckUploadTypeExtensions_ReportsTheSameMistake(t *testing.T) {
	byType := map[string][]string{
		"image/png":       {"png"},
		"application/pdf": {},
		"text/plain":      {".TXT"},
	}
	for range 20 {
		err := CheckUploadTypeExtensions(byType)
		if err == nil || !strings.Contains(err.Error(), `"application/pdf" lists no extensions`) {
			t.Fatalf("CheckUploadTypeExtensions() = %v, want the mistake in application/pdf", err)
		}
	}
}

func TestExtensionMismatchError_Message(t *testing.T) {
	err := &ExtensionMismatchError{Extension: ".exe", MediaType: "application/pdf"}
	if got, want := err.Error(), `storage: file name extension ".exe" does not match type "application/pdf"`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
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

func TestUploadService_Save(t *testing.T) {
	mock := &MockDriver{}
	content := []byte("<certificate/>")

	metadata, err := NewService(mock).Save(context.Background(), "certificate.xml", "application/xml", bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if metadata.Name != "certificate.xml" || metadata.MimeType != "application/xml" || metadata.Size != int64(len(content)) {
		t.Errorf("unexpected metadata: %+v", metadata)
	}
	if metadata.Key != metadata.ID+".xml" {
		t.Errorf("expected key %s.xml, got %s", metadata.ID, metadata.Key)
	}
	if metadata.UploadURL != "" {
		t.Errorf("expected no upload URL, got %s", metadata.UploadURL)
	}
	if mock.SavedKey != metadata.Key || mock.SavedContentType != "application/xml" || !bytes.Equal(mock.SavedBody, content) {
		t.Errorf("driver got key %q, type %q, body %q", mock.SavedKey, mock.SavedContentType, mock.SavedBody)
	}
}

func TestUploadService_Save_DefaultMime(t *testing.T) {
	mock := &MockDriver{}

	metadata, err := NewService(mock).Save(context.Background(), "blob", "", bytes.NewReader([]byte("data")), 4)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if metadata.MimeType != drivers.DefaultMime || mock.SavedContentType != drivers.DefaultMime {
		t.Errorf("expected %s, got metadata %q and driver %q", drivers.DefaultMime, metadata.MimeType, mock.SavedContentType)
	}
}

func TestUploadService_Save_IgnoresUploadLimits(t *testing.T) {
	// The upload limits govern what clients may upload; a service decides for
	// itself what it stores (#271).
	mock := &MockDriver{}
	service := NewService(mock, WithAllowedUploadTypes("application/pdf"), WithMaxUploadSize(10))
	content := bytes.Repeat([]byte("x"), 100)

	if _, err := service.Save(context.Background(), "report.xml", "text/xml", bytes.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if !bytes.Equal(mock.SavedBody, content) {
		t.Error("driver did not receive the content")
	}
}

func TestUploadService_Save_DriverError(t *testing.T) {
	want := errors.New("disk full")

	_, err := NewService(&MockDriver{SaveErr: want}).Save(context.Background(), "report.pdf", "application/pdf", bytes.NewReader([]byte("data")), 4)
	if !errors.Is(err, want) {
		t.Fatalf("expected the driver error, got %v", err)
	}
}

func TestUploadService_Save_RoundTrip(t *testing.T) {
	driver, err := drivers.NewLocalFSDriver(t.TempDir(), "http://localhost:8080", "secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewLocalFSDriver: %v", err)
	}
	service := NewService(driver)
	content := []byte("%PDF-1.7 generated")

	metadata, err := service.Save(context.Background(), "receipt.pdf", "application/pdf", bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	body, contentType, err := service.Download(context.Background(), metadata.Key)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("reading the downloaded body: %v", err)
	}
	if !bytes.Equal(got, content) || contentType != "application/pdf" {
		t.Errorf("Download returned %q (%s), want %q (application/pdf)", got, contentType, content)
	}
}
