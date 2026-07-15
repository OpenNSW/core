// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package s3

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenNSW/core/artifact"
)

func TestNewInvalidConfig(t *testing.T) {
	// Missing Region — New must validate and fail without building a client.
	if _, err := New(context.Background(), Config{Bucket: "artifacts"}); err == nil {
		t.Error("expected error for invalid config, got nil")
	}
}

func TestNewValidConfig(t *testing.T) {
	// Static credentials keep AWS config loading offline (no credential-chain
	// or IMDS lookups), so New should succeed without network access.
	loader, err := New(context.Background(), Config{
		Bucket: "artifacts", Region: "us-east-1",
		AccessKey: "ak", SecretKey: "sk",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if loader == nil {
		t.Fatal("New returned nil loader")
	}
}

const testBucket = "artifacts"

// newTestLoader builds a Loader pointed at endpoint. Setting Endpoint also
// exercises the custom-endpoint / path-style branch in New.
func newTestLoader(t *testing.T, endpoint string) *Loader {
	t.Helper()
	l, err := New(context.Background(), Config{
		Bucket:    testBucket,
		Region:    "us-east-1",
		Endpoint:  endpoint,
		AccessKey: "ak",
		SecretKey: "sk",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

// s3Error writes an S3-style XML error so the AWS SDK deserializes it into the
// matching typed error (e.g. code "NoSuchKey" -> *types.NoSuchKey).
func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>`+
		`<Error><Code>`+code+`</Code><Message>`+code+`</Message></Error>`)
}

func TestLoadSuccess(t *testing.T) {
	want := []byte(`{"ok":true}`)
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write(want)
	}))
	t.Cleanup(srv.Close)

	l := newTestLoader(t, srv.URL)
	got, err := l.Load(context.Background(), "workflows/import.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("body = %q, want %q", got, want)
	}
	// Path-style addressing: GET /{bucket}/{key}.
	if want := "/" + testBucket + "/workflows/import.json"; gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
}

func TestLoadNotFoundReturnsErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s3Error(w, http.StatusNotFound, "NoSuchKey")
	}))
	t.Cleanup(srv.Close)

	l := newTestLoader(t, srv.URL)
	_, err := l.Load(context.Background(), "missing.json")
	if !errors.Is(err, artifact.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestLoadOtherErrorIsNotErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// AccessDenied (403) is a genuine failure, not a missing object, and is
		// not retried by the SDK.
		s3Error(w, http.StatusForbidden, "AccessDenied")
	}))
	t.Cleanup(srv.Close)

	l := newTestLoader(t, srv.URL)
	_, err := l.Load(context.Background(), "denied.json")
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
	if errors.Is(err, artifact.ErrNotFound) {
		t.Errorf("403 should not map to ErrNotFound, got %v", err)
	}
}

func TestLoadTraversalWithoutPrefixReturnsErrNotFound(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte("x"))
	}))
	t.Cleanup(srv.Close)

	l := newTestLoader(t, srv.URL) // no Prefix
	_, err := l.Load(context.Background(), "../../etc/passwd")
	if !errors.Is(err, artifact.ErrNotFound) {
		t.Errorf("expected ErrNotFound for traversing key, got %v", err)
	}
	if hits != 0 {
		t.Errorf("expected no S3 request for traversing key, got %d", hits)
	}
}

func TestLoadEmptyKeyReturnsErrNotFound(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte("x"))
	}))
	t.Cleanup(srv.Close)

	// With a prefix, an empty or "." key must not collapse to the prefix itself.
	l, err := New(context.Background(), Config{
		Bucket: testBucket, Region: "us-east-1", Endpoint: srv.URL,
		AccessKey: "ak", SecretKey: "sk", Prefix: "deployment-a",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, key := range []string{"", ".", "   "} {
		_, err := l.Load(context.Background(), key)
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("Load(%q): expected ErrNotFound, got %v", key, err)
		}
	}
	if hits != 0 {
		t.Errorf("expected no S3 request for empty keys, got %d", hits)
	}
}

func TestLoadWithPrefix(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("x"))
	}))
	t.Cleanup(srv.Close)

	l, err := New(context.Background(), Config{
		Bucket: testBucket, Region: "us-east-1", Endpoint: srv.URL,
		AccessKey: "ak", SecretKey: "sk", Prefix: "deployment-a",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := l.Load(context.Background(), "manifest.json"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The prefix is prepended to the key: GET /{bucket}/{prefix}/{key}.
	if want := "/" + testBucket + "/deployment-a/manifest.json"; gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
}

func TestLoadEscapingPrefixReturnsErrNotFoundWithoutRequest(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte("x"))
	}))
	t.Cleanup(srv.Close)

	l, err := New(context.Background(), Config{
		Bucket: testBucket, Region: "us-east-1", Endpoint: srv.URL,
		AccessKey: "ak", SecretKey: "sk", Prefix: "deployment-a",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = l.Load(context.Background(), "../deployment-b/secret.json")
	if !errors.Is(err, artifact.ErrNotFound) {
		t.Errorf("expected ErrNotFound for escaping key, got %v", err)
	}
	if hits != 0 {
		t.Errorf("expected no S3 request for escaping key, got %d", hits)
	}
}
