// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3Driver_PresignTTL(t *testing.T) {
	client := s3.New(s3.Options{Region: "us-east-1"})

	if got := NewS3Driver(client, "uploads", "", 5*time.Minute).PresignTTL(); got != 5*time.Minute {
		t.Errorf("PresignTTL() = %v, want %v", got, 5*time.Minute)
	}
	if got := NewS3Driver(client, "uploads", "", 0).PresignTTL(); got != DefaultPresignTTL {
		t.Errorf("PresignTTL() = %v, want %v", got, DefaultPresignTTL)
	}
}

// fakeObject is what a fake bucket answers a HEAD request for a key with.
type fakeObject struct {
	status      int
	contentType string
}

// newFakeS3Driver returns an S3Driver for bucket "uploads", on path-style
// URLs, against a server that answers HEAD requests from objects and
// rejects every other method: the driver only ever looks a file up, and
// presigning makes no request.
func newFakeS3Driver(t *testing.T, objects map[string]fakeObject) *S3Driver {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		obj, ok := objects[strings.TrimPrefix(r.URL.Path, "/uploads/")]
		if !ok {
			obj = fakeObject{status: http.StatusNotFound}
		}
		if obj.contentType != "" {
			w.Header().Set("Content-Type", obj.contentType)
		}
		w.WriteHeader(obj.status)
	}))
	t.Cleanup(srv.Close)

	client := s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", ""),
		Retryer:      aws.NopRetryer{},
	})
	return NewS3Driver(client, "uploads", "", time.Minute)
}

func TestS3Driver_GetDownloadURL(t *testing.T) {
	const override = "response-content-disposition"

	driver := newFakeS3Driver(t, map[string]fakeObject{
		"doc.xml":     {http.StatusOK, "text/xml"},
		"data.xml":    {http.StatusOK, "application/xml; charset=utf-8"},
		"image.svg":   {http.StatusOK, "image/svg+xml"},
		"scan.pdf":    {http.StatusOK, "application/pdf"},
		"permit.html": {http.StatusOK, "text/html; charset=utf-8"},
		"untyped":     {http.StatusOK, ""},
	})

	tests := []struct {
		key            string
		wantAttachment bool
	}{
		{"doc.xml", true},
		{"data.xml", true},
		{"image.svg", true},
		{"scan.pdf", false},
		{"permit.html", false},
		{"untyped", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			rawURL, err := driver.GetDownloadURL(t.Context(), tt.key)
			if err != nil {
				t.Fatalf("GetDownloadURL: %v", err)
			}
			u, err := url.Parse(rawURL)
			if err != nil {
				t.Fatalf("parse %q: %v", rawURL, err)
			}
			if want := "/uploads/" + tt.key; u.Path != want {
				t.Errorf("path = %q, want %q", u.Path, want)
			}
			q := u.Query()
			if q.Get("X-Amz-Signature") == "" {
				t.Error("URL is not signed")
			}
			if tt.wantAttachment {
				if got := q.Get(override); got != "attachment" {
					t.Errorf("%s = %q, want attachment", override, got)
				}
			} else if q.Has(override) {
				t.Errorf("%s = %q, want none", override, q.Get(override))
			}
		})
	}
}

func TestS3Driver_GetDownloadURL_LookupFailures(t *testing.T) {
	driver := newFakeS3Driver(t, map[string]fakeObject{
		"denied": {status: http.StatusForbidden},
		"broken": {status: http.StatusInternalServerError},
	})

	tests := []struct {
		key          string
		wantNotFound bool
	}{
		{"missing", true},
		{"denied", false},
		{"broken", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, err := driver.GetDownloadURL(t.Context(), tt.key)
			if err == nil {
				t.Fatalf("GetDownloadURL = %q, want an error", got)
			}
			if got != "" {
				t.Errorf("URL = %q, want none alongside the error", got)
			}
			if errors.Is(err, ErrNotFound) != tt.wantNotFound {
				t.Errorf("errors.Is(err, ErrNotFound) = %v, want %v (err: %v)", !tt.wantNotFound, tt.wantNotFound, err)
			}
		})
	}
}
