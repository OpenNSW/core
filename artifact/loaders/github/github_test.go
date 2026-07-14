// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/loaders/github"
)

// capture records the last request newServer saw. The server serves body for
// any path that does not end in "/missing.json" (which yields 404).
type capture struct {
	path, ref, accept, auth string
	hits                    int
}

func newServer(t *testing.T, body string, status int) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits++
		c.path = r.URL.Path
		c.ref = r.URL.Query().Get("ref")
		c.accept = r.Header.Get("Accept")
		c.auth = r.Header.Get("Authorization")
		if strings.HasSuffix(r.URL.Path, "/missing.json") {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		if status != http.StatusOK {
			http.Error(w, "boom", status)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func TestLoadSuccess(t *testing.T) {
	srv, rec := newServer(t, `{"ok":true}`, http.StatusOK)
	l, err := github.New(github.Config{
		Owner: "org", Repo: "config", Ref: "v1.2.0",
		Token: "secret", BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	data, err := l.Load(context.Background(), "workflows/import.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(data) != `{"ok":true}` {
		t.Errorf("body = %q", data)
	}
	if want := "/repos/org/config/contents/workflows/import.json"; rec.path != want {
		t.Errorf("path = %q, want %q", rec.path, want)
	}
	if rec.ref != "v1.2.0" {
		t.Errorf("ref = %q, want v1.2.0", rec.ref)
	}
	if rec.accept != "application/vnd.github.raw" {
		t.Errorf("Accept = %q", rec.accept)
	}
	if rec.auth != "Bearer secret" {
		t.Errorf("Authorization = %q, want Bearer secret", rec.auth)
	}
}

func TestLoadNoTokenOmitsAuthHeader(t *testing.T) {
	srv, rec := newServer(t, `x`, http.StatusOK)
	l, _ := github.New(github.Config{Owner: "org", Repo: "config", Ref: "main", BaseURL: srv.URL})
	if _, err := l.Load(context.Background(), "a.json"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.auth != "" {
		t.Errorf("Authorization = %q, want empty", rec.auth)
	}
}

func TestLoadBasePath(t *testing.T) {
	srv, rec := newServer(t, `x`, http.StatusOK)
	l, _ := github.New(github.Config{
		Owner: "org", Repo: "config", Ref: "main",
		BasePath: "deployment-a", BaseURL: srv.URL,
	})

	if _, err := l.Load(context.Background(), "manifest.json"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := "/repos/org/config/contents/deployment-a/manifest.json"; rec.path != want {
		t.Errorf("path = %q, want %q", rec.path, want)
	}
}

func TestLoadRawHost(t *testing.T) {
	srv, rec := newServer(t, `raw-bytes`, http.StatusOK)
	l, err := github.New(github.Config{
		Owner: "org", Repo: "config", Ref: "v1.2.0",
		BasePath: "deployment-a", UseRawHost: true, RawBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	data, err := l.Load(context.Background(), "workflows/import.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(data) != "raw-bytes" {
		t.Errorf("body = %q", data)
	}
	// Raw host: ref is in the path, no /repos/.../contents/, no ref query, no Accept.
	if want := "/org/config/v1.2.0/deployment-a/workflows/import.json"; rec.path != want {
		t.Errorf("path = %q, want %q", rec.path, want)
	}
	if rec.ref != "" {
		t.Errorf("ref query = %q, want empty (ref is in the path)", rec.ref)
	}
	if rec.accept != "" {
		t.Errorf("Accept = %q, want empty for raw host", rec.accept)
	}
}

func TestLoadRawHostMissingReturnsErrNotFound(t *testing.T) {
	srv, _ := newServer(t, "", http.StatusOK)
	l, _ := github.New(github.Config{
		Owner: "org", Repo: "config", Ref: "main",
		UseRawHost: true, RawBaseURL: srv.URL,
	})

	_, err := l.Load(context.Background(), "missing.json")
	if !errors.Is(err, artifact.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestLoadMissingReturnsErrNotFound(t *testing.T) {
	srv, _ := newServer(t, "", http.StatusOK)
	l, _ := github.New(github.Config{Owner: "org", Repo: "config", Ref: "main", BaseURL: srv.URL})

	_, err := l.Load(context.Background(), "missing.json")
	if !errors.Is(err, artifact.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestLoadEscapingBasePathReturnsErrNotFoundWithoutRequest(t *testing.T) {
	srv, rec := newServer(t, "", http.StatusOK)
	l, _ := github.New(github.Config{
		Owner: "org", Repo: "config", Ref: "main",
		BasePath: "deployment-a", BaseURL: srv.URL,
	})

	_, err := l.Load(context.Background(), "../deployment-b/secret.json")
	if !errors.Is(err, artifact.ErrNotFound) {
		t.Errorf("expected ErrNotFound for escaping path, got %v", err)
	}
	if rec.hits != 0 {
		t.Errorf("expected no HTTP request for escaping path, got %d", rec.hits)
	}
}

func TestLoadTraversalWithoutBasePathReturnsErrNotFound(t *testing.T) {
	srv, rec := newServer(t, "", http.StatusOK)
	l, _ := github.New(github.Config{Owner: "org", Repo: "config", Ref: "main", BaseURL: srv.URL})

	_, err := l.Load(context.Background(), "../../etc/passwd")
	if !errors.Is(err, artifact.ErrNotFound) {
		t.Errorf("expected ErrNotFound for traversing path, got %v", err)
	}
	if rec.hits != 0 {
		t.Errorf("expected no HTTP request for traversing path, got %d", rec.hits)
	}
}

func TestLoadServerErrorIsNotErrNotFound(t *testing.T) {
	srv, _ := newServer(t, "", http.StatusInternalServerError)
	l, _ := github.New(github.Config{Owner: "org", Repo: "config", Ref: "main", BaseURL: srv.URL})

	_, err := l.Load(context.Background(), "a.json")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if errors.Is(err, artifact.ErrNotFound) {
		t.Errorf("500 should not map to ErrNotFound, got %v", err)
	}
}

func TestNewInvalidConfig(t *testing.T) {
	if _, err := github.New(github.Config{Owner: "org", Repo: "config"}); err == nil {
		t.Error("expected error for missing Ref")
	}
}
