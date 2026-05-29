package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/OpenNSW/nsw/backend/internal/auth"
	"github.com/OpenNSW/nsw/backend/pkg/storage/drivers"
)

// ... existing code ...

func TestDownloadContent_LocalDriver_Success(t *testing.T) {
	tempDir := t.TempDir()
	driver, _ := drivers.NewLocalFSDriver(tempDir, "/api/v1/storage", "local-dev-secret", 15*time.Minute)
	service := NewService(driver)
	handler := NewHTTPHandler(service)

	ctx := context.Background()
	key := "550e8400-e29b-41d4-a716-446655440000.pdf"
	content := []byte("test content")
	if err := driver.Save(ctx, key, bytes.NewReader(content), "application/pdf"); err != nil {
		t.Fatalf("failed to save test file: %v", err)
	}

	// Generate valid signed URL using the driver
	downloadURL, err := driver.GetDownloadURL(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get download URL: %v", err)
	}

	parsedURL, err := url.Parse(downloadURL)
	if err != nil {
		t.Fatalf("Failed to parse download URL: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, parsedURL.String(), nil)
	req.SetPathValue("key", key)
	rec := httptest.NewRecorder()

	// No auth context set — should still succeed because this endpoint is signature-secured instead of auth-secured.
	handler.DownloadContent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	if rec.Header().Get("Content-Type") != "application/pdf" {
		t.Errorf("expected Content-Type application/pdf, got %s", rec.Header().Get("Content-Type"))
	}

	if !bytes.Equal(rec.Body.Bytes(), content) {
		t.Error("body does not match")
	}
}

// withAuthContext returns a context with the given AuthContext injected.
func withAuthContext(ctx context.Context, ac *auth.AuthContext) context.Context {
	return context.WithValue(ctx, auth.AuthContextKey, ac)
}

func TestDownload_MissingKey(t *testing.T) {
	handler := NewHTTPHandler(NewService(&MockDriver{}))

	req := httptest.NewRequest(http.MethodGet, "/files/", nil)
	// Auth present, but no path value for "key".
	ctx := withAuthContext(req.Context(), &auth.AuthContext{
		User: &auth.UserContext{ID: "trader-1"},
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.Download(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
}

func TestDownload_Success(t *testing.T) {
	mock := &MockDriver{}
	handler := NewHTTPHandler(NewService(mock))

	// Build request with auth context and path value.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /files/{key}", handler.Download)

	req := httptest.NewRequest(http.MethodGet, "/files/550e8400-e29b-41d4-a716-446655440000.pdf", nil)
	ctx := withAuthContext(req.Context(), &auth.AuthContext{
		User: &auth.UserContext{ID: "trader-1"},
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if _, ok := resp["download_url"]; !ok {
		t.Error("response missing 'download_url' field")
	}
	if _, ok := resp["expires_at"]; !ok {
		t.Error("response missing 'expires_at' field")
	}

	url, _ := resp["download_url"].(string)
	if url != "/test/download/550e8400-e29b-41d4-a716-446655440000.pdf" {
		t.Errorf("unexpected download_url: %s", url)
	}
}

func TestDownload_GenerateURLError(t *testing.T) {
	mock := &MockDriver{
		GenerateURLErr: errors.New("presign failure"),
	}
	handler := NewHTTPHandler(NewService(mock))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /files/{key}", handler.Download)

	req := httptest.NewRequest(http.MethodGet, "/files/550e8400-e29b-41d4-a716-446655440000", nil)
	ctx := withAuthContext(req.Context(), &auth.AuthContext{
		User: &auth.UserContext{ID: "trader-1"},
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}

	body := rec.Body.String()
	if body == "" {
		t.Fatal("expected error body, got empty")
	}
}

func TestDownload_InvalidKeyFormat(t *testing.T) {
	handler := NewHTTPHandler(NewService(&MockDriver{}))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /files/{key}", handler.Download)

	// Key that is not UUID or UUID.ext (validStorageKey rejects it)
	req := httptest.NewRequest(http.MethodGet, "/files/invalid-key-format", nil)
	ctx := withAuthContext(req.Context(), &auth.AuthContext{
		User: &auth.UserContext{ID: "trader-1"},
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid key, got %d", rec.Code)
	}
}

func TestUpload_Unauthorized(t *testing.T) {
	handler := NewHTTPHandler(NewService(&MockDriver{}))

	body := map[string]any{
		"filename":  "test.pdf",
		"mime_type": "application/pdf",
		"size":      1024,
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/uploads", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Upload(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}
}

func TestUpload_Success(t *testing.T) {
	mock := &MockDriver{}
	handler := NewHTTPHandler(NewService(mock))

	body := map[string]any{
		"filename":  "test.pdf",
		"mime_type": "application/pdf",
		"size":      1024,
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/uploads", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	ctx := withAuthContext(req.Context(), &auth.AuthContext{
		User: &auth.UserContext{ID: "trader-1"},
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.Upload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	var metadata FileMetadata
	if err := json.NewDecoder(rec.Body).Decode(&metadata); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if metadata.Name != "test.pdf" {
		t.Errorf("expected name test.pdf, got %s", metadata.Name)
	}
	if metadata.UploadURL == "" {
		t.Error("expected upload_url to be populated")
	}
}

func TestUploadContentLocal_Success(t *testing.T) {
	tempDir := t.TempDir()
	driver, _ := drivers.NewLocalFSDriver(tempDir, "/api/v1/storage", "local-dev-secret", 15*time.Minute)
	service := NewService(driver)
	handler := NewHTTPHandler(service)

	key := "550e8400-e29b-41d4-a716-446655440000.pdf"
	content := []byte("pdf content")

	// Generate valid upload URL using the driver
	contentType := "application/pdf"
	maxSizeBytes := int64(32 << 20)

	uploadURL, err := driver.GetUploadURL(context.Background(), key, contentType, maxSizeBytes)
	if err != nil {
		t.Fatalf("Failed to get upload URL: %v", err)
	}

	parsedURL, err := url.Parse(uploadURL)
	if err != nil {
		t.Fatalf("Failed to parse upload URL: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, parsedURL.RequestURI(), bytes.NewReader(content))
	req.SetPathValue("key", key)
	req.Header.Set("Content-Type", "application/pdf")
	rec := httptest.NewRecorder()

	handler.UploadContentLocal(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	// Verify file was saved
	reader, ct, err := driver.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("failed to get saved file: %v", err)
	}
	defer reader.Close()
	if ct != "application/pdf" {
		t.Errorf("expected content type application/pdf, got %s", ct)
	}
	savedContent, _ := io.ReadAll(reader)
	if !bytes.Equal(savedContent, content) {
		t.Error("saved content does not match")
	}
}

func TestDelete_Unauthorized(t *testing.T) {
	handler := NewHTTPHandler(NewService(&MockDriver{}))

	req := httptest.NewRequest(http.MethodDelete, "/storage/550e8400-e29b-41d4-a716-446655440000.pdf", nil)
	req.SetPathValue("key", "550e8400-e29b-41d4-a716-446655440000.pdf")
	rec := httptest.NewRecorder()

	handler.Delete(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}
}

func TestDownloadContent_NonLocalDriver_NotFound(t *testing.T) {
	// For non-local drivers, DownloadContent should be disabled and return 404
	handler := NewHTTPHandler(NewService(&MockDriver{}))

	req := httptest.NewRequest(http.MethodGet, "/storage/550e8400-e29b-41d4-a716-446655440000.pdf/content", nil)
	req.SetPathValue("key", "550e8400-e29b-41d4-a716-446655440000.pdf")
	rec := httptest.NewRecorder()

	handler.DownloadContent(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
}
