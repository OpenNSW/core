// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OpenNSW/core/authn"
	"github.com/OpenNSW/core/storage/drivers"
)

// ... existing code ...

// withAuthContext returns a context with the given AuthContext injected.
func withAuthContext(ctx context.Context, ac *authn.AuthContext) context.Context {
	return context.WithValue(ctx, authn.AuthContextKey, ac)
}

func TestDownload_MissingKey(t *testing.T) {
	handler := NewHTTPHandler(NewService(&MockDriver{}))

	req := httptest.NewRequest(http.MethodGet, "/files/", nil)
	// Auth present, but no path value for "key".
	ctx := withAuthContext(req.Context(), &authn.AuthContext{
		User: &authn.UserContext{ID: "trader-1"},
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
	ctx := withAuthContext(req.Context(), &authn.AuthContext{
		User: &authn.UserContext{ID: "trader-1"},
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
	ctx := withAuthContext(req.Context(), &authn.AuthContext{
		User: &authn.UserContext{ID: "trader-1"},
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

func TestDownload_ExpiresAtMatchesDriverTTL(t *testing.T) {
	localDriver, err := drivers.NewLocalFSDriver(t.TempDir(), "/api/v1/storage", "local-dev-secret", 5*time.Minute)
	if err != nil {
		t.Fatalf("NewLocalFSDriver: %v", err)
	}

	tests := []struct {
		name   string
		driver StorageDriver
		ttl    time.Duration
	}{
		{name: "driver reports its TTL", driver: localDriver, ttl: 5 * time.Minute},
		{name: "driver without a TTL falls back to the default", driver: &MockDriver{}, ttl: drivers.DefaultPresignTTL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /files/{key}", NewHTTPHandler(NewService(tt.driver)).Download)

			req := httptest.NewRequest(http.MethodGet, "/files/550e8400-e29b-41d4-a716-446655440000.pdf", nil)
			rec := httptest.NewRecorder()

			before := time.Now()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
			}

			var resp struct {
				ExpiresAt int64 `json:"expires_at"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			want := before.Add(tt.ttl).Unix()
			if resp.ExpiresAt < want || resp.ExpiresAt > want+2 {
				t.Errorf("expires_at = %d, want about %d (now + %v)", resp.ExpiresAt, want, tt.ttl)
			}
		})
	}
}

func TestDownload_InvalidKeyFormat(t *testing.T) {
	handler := NewHTTPHandler(NewService(&MockDriver{}))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /files/{key}", handler.Download)

	// Key that is not UUID or UUID.ext (validStorageKey rejects it)
	req := httptest.NewRequest(http.MethodGet, "/files/invalid-key-format", nil)
	ctx := withAuthContext(req.Context(), &authn.AuthContext{
		User: &authn.UserContext{ID: "trader-1"},
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

func TestUpload_PolicyErrors(t *testing.T) {
	tests := []struct {
		name     string
		opts     []ServiceOption
		mimeType string
		size     int64
		want     int
		wantErr  string
	}{
		{name: "any type by default", mimeType: "application/x-msdownload", size: 1024, want: http.StatusOK},
		{name: "type outside allowlist", opts: []ServiceOption{WithAllowedContentTypes("application/pdf")}, mimeType: "application/x-msdownload", size: 1024, want: http.StatusUnsupportedMediaType, wantErr: "invalid or prohibited file type"},
		{name: "size missing", mimeType: "application/pdf", size: 0, want: http.StatusBadRequest, wantErr: "size must be greater than 0"},
		{name: "over the default limit", mimeType: "application/pdf", size: 32<<20 + 1, want: http.StatusBadRequest, wantErr: "file size exceeds 32MB limit"},
		{name: "over a custom limit", opts: []ServiceOption{WithMaxUploadSize(1 << 20)}, mimeType: "application/pdf", size: 1<<20 + 1, want: http.StatusBadRequest, wantErr: "file size exceeds 1MB limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHTTPHandler(NewService(&MockDriver{}, tt.opts...))

			jsonBody, _ := json.Marshal(map[string]any{
				"filename":  "upload.bin",
				"mime_type": tt.mimeType,
				"size":      tt.size,
			})
			req := httptest.NewRequest(http.MethodPost, "/uploads", bytes.NewReader(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(withAuthContext(req.Context(), &authn.AuthContext{
				User: &authn.UserContext{ID: "trader-1"},
			}))
			rec := httptest.NewRecorder()

			handler.Upload(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("expected status %d, got %d. Body: %s", tt.want, rec.Code, rec.Body.String())
			}
			if tt.wantErr != "" {
				var resp map[string]string
				if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
					t.Fatalf("failed to decode error body: %v", err)
				}
				if resp["error"] != tt.wantErr {
					t.Errorf("expected error %q, got %q", tt.wantErr, resp["error"])
				}
			}
		})
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
	ctx := withAuthContext(req.Context(), &authn.AuthContext{
		User: &authn.UserContext{ID: "trader-1"},
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

func TestDelete_InvalidKeyFormat(t *testing.T) {
	mock := &MockDriver{}
	handler := NewHTTPHandler(NewService(mock))

	req := httptest.NewRequest(http.MethodDelete, "/storage/invalid-key-format", nil)
	req.SetPathValue("key", "invalid-key-format")
	req = req.WithContext(withAuthContext(req.Context(), &authn.AuthContext{
		User: &authn.UserContext{ID: "trader-1"},
	}))
	rec := httptest.NewRecorder()

	handler.Delete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
	if mock.DeleteCalled {
		t.Error("Delete reached the driver with an invalid key")
	}
}
