// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
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
	size        int64
}

// fakeS3 is an S3Driver for bucket "uploads", on path-style URLs, against a
// server that answers HEAD requests from objects and rejects every other
// method. requests counts the requests the server received.
type fakeS3 struct {
	*S3Driver
	requests *atomic.Int32
}

func newFakeS3(t *testing.T, objects map[string]fakeObject) fakeS3 {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
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
		w.Header().Set("Content-Length", strconv.FormatInt(obj.size, 10))
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
	return fakeS3{S3Driver: NewS3Driver(client, "uploads", "", time.Minute), requests: &requests}
}

func TestS3Driver_Stat(t *testing.T) {
	driver := newFakeS3(t, map[string]fakeObject{
		"doc.xml":  {http.StatusOK, "text/xml", 4},
		"scan.pdf": {http.StatusOK, "application/pdf", 1024},
		"untyped":  {http.StatusOK, "", 7},
	})

	tests := []struct {
		key  string
		want ObjectInfo
	}{
		{"doc.xml", ObjectInfo{ContentType: "text/xml", Size: 4}},
		{"scan.pdf", ObjectInfo{ContentType: "application/pdf", Size: 1024}},
		{"untyped", ObjectInfo{Size: 7}},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, err := driver.Stat(t.Context(), tt.key)
			if err != nil {
				t.Fatalf("Stat: %v", err)
			}
			if got != tt.want {
				t.Errorf("Stat = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestS3Driver_Stat_Failures(t *testing.T) {
	driver := newFakeS3(t, map[string]fakeObject{
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
			info, err := driver.Stat(t.Context(), tt.key)
			if err == nil {
				t.Fatalf("Stat = %+v, want an error", info)
			}
			if errors.Is(err, ErrNotFound) != tt.wantNotFound {
				t.Errorf("errors.Is(err, ErrNotFound) = %v, want %v (err: %v)", !tt.wantNotFound, tt.wantNotFound, err)
			}
		})
	}
}

func TestS3Driver_GetDownloadURL(t *testing.T) {
	const (
		key      = "550e8400-e29b-41d4-a716-446655440000.xml"
		override = "response-content-disposition"
	)
	driver := newFakeS3(t, nil)

	tests := []struct {
		disposition  string
		wantOverride string // empty: the URL must carry none
	}{
		{DispositionAttachment, "attachment"},
		{DispositionInline, ""},
	}
	for _, tt := range tests {
		t.Run(tt.disposition, func(t *testing.T) {
			rawURL, err := driver.GetDownloadURL(t.Context(), key, tt.disposition)
			if err != nil {
				t.Fatalf("GetDownloadURL: %v", err)
			}
			u, err := url.Parse(rawURL)
			if err != nil {
				t.Fatalf("parse %q: %v", rawURL, err)
			}
			if want := "/uploads/" + key; u.Path != want {
				t.Errorf("path = %q, want %q", u.Path, want)
			}
			q := u.Query()
			if q.Get("X-Amz-Signature") == "" {
				t.Error("URL is not signed")
			}
			if tt.wantOverride == "" {
				if q.Has(override) {
					t.Errorf("%s = %q, want none", override, q.Get(override))
				}
			} else if got := q.Get(override); got != tt.wantOverride {
				t.Errorf("%s = %q, want %q", override, got, tt.wantOverride)
			}
		})
	}

	// The driver signs what it is told and looks nothing up.
	if got := driver.requests.Load(); got != 0 {
		t.Errorf("GetDownloadURL made %d requests to the bucket, want none", got)
	}
}

func TestS3Driver_GetDownloadURL_RejectsUnknownDisposition(t *testing.T) {
	driver := newFakeS3(t, nil)
	for _, disposition := range []string{"", "Inline", "attachment; filename=x.pdf"} {
		if got, err := driver.GetDownloadURL(t.Context(), "550e8400-e29b-41d4-a716-446655440000.pdf", disposition); err == nil {
			t.Errorf("GetDownloadURL(%q) = %q, want an error", disposition, got)
		}
	}
}
