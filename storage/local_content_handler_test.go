// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OpenNSW/core/storage/drivers"
)

const routeTestKey = "550e8400-e29b-41d4-a716-446655440000.pdf"

// newRoutedDriver returns a local driver whose PublicURL is a running server
// that serves its content routes through LocalContentHandler, so the URLs it
// issues are exercised exactly as a client would use them.
func newRoutedDriver(t *testing.T, opts ...drivers.LocalOption) *drivers.LocalFSDriver {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	driver, err := drivers.NewLocalFSDriver(t.TempDir(), srv.URL, "local-dev-secret", 15*time.Minute, opts...)
	if err != nil {
		t.Fatalf("NewLocalFSDriver: %v", err)
	}
	NewLocalContentHandler(driver).RegisterRoutes(mux)
	return driver
}

// contentURL builds a URL on the driver's content route by hand, for requests
// the driver would never issue itself.
func contentURL(driver *drivers.LocalFSDriver, key string, query url.Values) string {
	return driver.PublicURL + strings.Replace(driver.ContentPattern(), "{key}", key, 1) + "?" + query.Encode()
}

func do(t *testing.T, method, rawURL, contentType string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestLocalContentRoutes_RoundTrip(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		contentType string
		opts        []drivers.LocalOption
		wantPrefix  string
	}{
		{name: "pdf", key: routeTestKey, contentType: "application/pdf", wantPrefix: "/api/v1/storage/"},
		{name: "html", key: "550e8400-e29b-41d4-a716-446655440000.html", contentType: "text/html", wantPrefix: "/api/v1/storage/"},
		{name: "custom route prefix", key: routeTestKey, contentType: "application/pdf", opts: []drivers.LocalOption{drivers.WithRoutePrefix("/files")}, wantPrefix: "/files/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver := newRoutedDriver(t, tt.opts...)
			ctx := context.Background()
			content := []byte("file content")

			uploadURL, err := driver.GetUploadURL(ctx, tt.key, tt.contentType, int64(len(content)))
			if err != nil {
				t.Fatalf("GetUploadURL: %v", err)
			}
			if want := driver.PublicURL + tt.wantPrefix + tt.key + "/content?"; !strings.HasPrefix(uploadURL, want) {
				t.Fatalf("upload URL %s does not start with %s", uploadURL, want)
			}
			if resp := do(t, http.MethodPut, uploadURL, tt.contentType, content); resp.StatusCode != http.StatusNoContent {
				t.Fatalf("PUT: expected 204, got %d", resp.StatusCode)
			}

			downloadURL, err := driver.GetDownloadURL(ctx, tt.key)
			if err != nil {
				t.Fatalf("GetDownloadURL: %v", err)
			}
			resp := do(t, http.MethodGet, downloadURL, "", nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET: expected 200, got %d", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Type"); got != tt.contentType {
				t.Errorf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := resp.Header.Get("Content-Security-Policy"); got != "sandbox" {
				t.Errorf("Content-Security-Policy = %q, want sandbox", got)
			}
			got, _ := io.ReadAll(resp.Body)
			if !bytes.Equal(got, content) {
				t.Errorf("body = %q, want %q", got, content)
			}
		})
	}
}

func TestLocalContentRoutes_RejectsBadUploads(t *testing.T) {
	driver := newRoutedDriver(t)
	ctx := context.Background()

	uploadURL, err := driver.GetUploadURL(ctx, routeTestKey, "application/pdf", 4)
	if err != nil {
		t.Fatalf("GetUploadURL: %v", err)
	}
	tampered, _ := url.Parse(uploadURL)
	q := tampered.Query()
	q.Set("maxSizeBytes", "1024")
	tampered.RawQuery = q.Encode()

	tests := []struct {
		name        string
		url         string
		contentType string
		body        []byte
		want        int
	}{
		{name: "content type differs from the signed one", url: uploadURL, contentType: "text/html", body: []byte("data"), want: http.StatusUnsupportedMediaType},
		{name: "constraint changed after signing", url: tampered.String(), contentType: "application/pdf", body: []byte("data"), want: http.StatusUnauthorized},
		{name: "body over the signed size", url: uploadURL, contentType: "application/pdf", body: []byte("too large"), want: http.StatusRequestEntityTooLarge},
		{name: "no token", url: contentURL(driver, routeTestKey, url.Values{}), contentType: "application/pdf", body: []byte("data"), want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if resp := do(t, http.MethodPut, tt.url, tt.contentType, tt.body); resp.StatusCode != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, resp.StatusCode)
			}
		})
	}
}

func TestLocalContentRoutes_RejectsBadDownloads(t *testing.T) {
	driver := newRoutedDriver(t)
	ctx := context.Background()

	// An upload URL must not open the file through the download route.
	uploadURL, err := driver.GetUploadURL(ctx, routeTestKey, "application/pdf", 4)
	if err != nil {
		t.Fatalf("GetUploadURL: %v", err)
	}

	past := time.Now().Add(-time.Minute).Unix()
	expired := contentURL(driver, routeTestKey, url.Values{
		"token":     {drivers.GenerateDownloadToken(routeTestKey, "local-dev-secret", past)},
		"expiresAt": {strconv.FormatInt(past, 10)},
	})

	tests := []struct {
		name string
		url  string
		want int
	}{
		{name: "upload token used to download", url: uploadURL, want: http.StatusUnauthorized},
		{name: "expired link", url: expired, want: http.StatusForbidden},
		{name: "no token", url: contentURL(driver, routeTestKey, url.Values{}), want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if resp := do(t, http.MethodGet, tt.url, "", nil); resp.StatusCode != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, resp.StatusCode)
			}
		})
	}
}

func TestLocalContentRoutes_RejectInvalidKeys(t *testing.T) {
	driver := newRoutedDriver(t)

	for _, method := range []string{http.MethodPut, http.MethodGet} {
		t.Run(method, func(t *testing.T) {
			resp := do(t, method, contentURL(driver, "not-a-storage-key", url.Values{"token": {"x"}, "expiresAt": {"1"}}), "application/pdf", []byte("data"))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", resp.StatusCode)
			}
		})
	}
}
