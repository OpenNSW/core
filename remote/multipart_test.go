// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readParts parses the request as multipart/form-data and returns each part in
// order, so a test can assert on names, filenames, per-part Content-Type and
// content the same way a real receiver would see them.
type receivedPart struct {
	Name        string
	FileName    string
	ContentType string
	Content     string
}

func readParts(t *testing.T, r *http.Request) []receivedPart {
	t.Helper()

	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	require.NotEmpty(t, params["boundary"], "Content-Type must carry the generated boundary")

	mr := multipart.NewReader(r.Body, params["boundary"])
	var parts []receivedPart
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		content, err := io.ReadAll(p)
		require.NoError(t, err)

		parts = append(parts, receivedPart{
			Name:        p.FormName(),
			FileName:    p.FileName(),
			ContentType: p.Header.Get("Content-Type"),
			Content:     string(content),
		})
	}
	return parts
}

func TestMultipartRequest_SendsPartsAndDecodesResponse(t *testing.T) {
	var got []receivedPart

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/documents/v1", r.URL.Path)
		got = readParts(t, r)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"a14341a6","status":"ACCEPTED"}`))
	}))
	defer server.Close()

	payload, err := JSONPart("payload", map[string]any{"submitter": "1"})
	require.NoError(t, err)

	var resp map[string]any
	err = NewClient(server.URL).MultipartRequest(context.Background(), MultipartRequest{
		Method: http.MethodPost,
		Path:   "/api/documents/v1",
		Parts: []Part{
			payload,
			{Name: "fileinfo", Content: []byte("1")},
			{Name: "file1", FileName: "invoice.pdf", ContentType: "application/pdf", Content: []byte("%PDF-1.4")},
		},
	}, &resp)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"id": "a14341a6", "status": "ACCEPTED"}, resp)

	require.Len(t, got, 3)

	// The JSON payload part carries a Content-Type but no filename.
	assert.Equal(t, "payload", got[0].Name)
	assert.Empty(t, got[0].FileName)
	assert.Equal(t, "application/json; charset=UTF-8", got[0].ContentType)
	assert.JSONEq(t, `{"submitter":"1"}`, got[0].Content)

	// A plain field carries neither, so a receiver reads it as a text field.
	assert.Equal(t, "fileinfo", got[1].Name)
	assert.Empty(t, got[1].FileName)
	assert.Empty(t, got[1].ContentType)
	assert.Equal(t, "1", got[1].Content)

	// A file part carries both, and keeps the declared filename verbatim so
	// receivers that match it against the payload can do so.
	assert.Equal(t, "file1", got[2].Name)
	assert.Equal(t, "invoice.pdf", got[2].FileName)
	assert.Equal(t, "application/pdf", got[2].ContentType)
	assert.Equal(t, "%PDF-1.4", got[2].Content)
}

func TestMultipartRequest_PreservesPartOrder(t *testing.T) {
	var names []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range readParts(t, r) {
			names = append(names, p.Name)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	err := NewClient(server.URL).MultipartRequest(context.Background(), MultipartRequest{
		Method: http.MethodPost,
		Parts: []Part{
			{Name: "payload", Content: []byte("{}")},
			{Name: "fileinfo", Content: []byte("2")},
			{Name: "file1", FileName: "a.pdf", Content: []byte("a")},
			{Name: "file2", FileName: "b.pdf", Content: []byte("b")},
		},
	}, nil)
	require.NoError(t, err)

	// Receivers that pair fileN with an ordered list in the payload depend on
	// parts arriving in the order they were given.
	assert.Equal(t, []string{"payload", "fileinfo", "file1", "file2"}, names)
}

func TestMultipartRequest_DecodesBodyOnErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"missing payload part"}`))
	}))
	defer server.Close()

	var resp map[string]any
	err := NewClient(server.URL).MultipartRequest(context.Background(), MultipartRequest{
		Method: http.MethodPost,
		Parts:  []Part{{Name: "fileinfo", Content: []byte("0")}},
	}, &resp)

	// A rejection is an error, but the caller still gets the service's reason:
	// interpreters turn that into a message rather than "we could not connect".
	require.Error(t, err)
	assert.Equal(t, "missing payload part", resp["error"])
}

func TestMultipartRequest_AppendsQueryParameters(t *testing.T) {
	var gotQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("mode")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	err := NewClient(server.URL).MultipartRequest(context.Background(), MultipartRequest{
		Method: http.MethodPost,
		Path:   "/api/documents/v1",
		Query:  map[string][]string{"mode": {"test"}},
		Parts:  []Part{{Name: "fileinfo", Content: []byte("0")}},
	}, nil)
	require.NoError(t, err)

	assert.Equal(t, "test", gotQuery)
}

func TestMultipartRequest_ReplaysBodyOnRetry(t *testing.T) {
	var bodies []int
	attempts := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := readParts(t, r)
		bodies = append(bodies, len(parts))
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	err := NewClient(server.URL).MultipartRequest(context.Background(), MultipartRequest{
		Method: http.MethodPost,
		Parts:  []Part{{Name: "payload", Content: []byte("{}")}},
		Retry: &RetryConfig{
			MaxRetries:      1,
			InitialBackoff:  time.Millisecond,
			MaxBackoff:      time.Millisecond,
			RetryableStatus: []int{http.StatusInternalServerError},
		},
	}, nil)
	require.NoError(t, err)

	// The retry must resend the full body, not an already-drained reader.
	require.Equal(t, 2, attempts)
	assert.Equal(t, []int{1, 1}, bodies)
}

func TestMultipartRequest_RejectsEmptyParts(t *testing.T) {
	err := NewClient("http://example.invalid").MultipartRequest(context.Background(), MultipartRequest{
		Method: http.MethodPost,
		Parts:  nil,
	}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one part")
}

func TestMultipartRequest_RejectsUnnamedPart(t *testing.T) {
	err := NewClient("http://example.invalid").MultipartRequest(context.Background(), MultipartRequest{
		Method: http.MethodPost,
		Parts:  []Part{{Content: []byte("x")}},
	}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no name")
}

func TestMultipartRequest_ClientSetsContentTypeOverCallerHeader(t *testing.T) {
	var gotContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		_ = readParts(t, r)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	err := NewClient(server.URL).MultipartRequest(context.Background(), MultipartRequest{
		Method:  http.MethodPost,
		Parts:   []Part{{Name: "payload", Content: []byte("{}")}},
		Headers: map[string]string{"Content-Type": "application/json", "X-Trace": "abc"},
	}, nil)
	require.NoError(t, err)

	// A caller-supplied Content-Type would strip the boundary and make the body
	// unparseable, so the generated one wins.
	assert.Contains(t, gotContentType, "multipart/form-data; boundary=")
}

func TestJSONPart_ReportsMarshalFailure(t *testing.T) {
	_, err := JSONPart("payload", make(chan int))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "payload")
}

func TestJSONPart_MarshalsValue(t *testing.T) {
	p, err := JSONPart("payload", map[string]string{"a": "b"})
	require.NoError(t, err)

	assert.Equal(t, "payload", p.Name)
	assert.Equal(t, "application/json; charset=UTF-8", p.ContentType)

	var decoded map[string]string
	require.NoError(t, json.Unmarshal(p.Content, &decoded))
	assert.Equal(t, map[string]string{"a": "b"}, decoded)
}

// A caller commonly forwards untrusted input into a part — an end user's
// original upload filename is the obvious case. mime/multipart writes header
// values verbatim, so a CR or LF reaching a header line would end it early and
// let the remainder be read as further headers, or as a forged part.
func TestMultipartRequest_NeutralisesHeaderInjectionInNameAndFileName(t *testing.T) {
	const injection = "evil.pdf\"\r\nContent-Type: application/json\r\nX-Injected: yes\r\n\r\n--INJECTED\r\n"

	for _, tc := range []struct {
		name string
		part Part
	}{
		{"via FileName", Part{Name: "file1", FileName: injection, ContentType: "application/pdf", Content: []byte("%PDF-1.4")}},
		{"via Name", Part{Name: injection, Content: []byte("x")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, contentType, err := buildMultipartBody([]Part{tc.part})
			require.NoError(t, err)

			// The injected text may still appear as inert characters inside the
			// quoted parameter. What must not survive is the CR/LF that would
			// end the header line and promote the rest to headers of its own.
			assert.NotContains(t, string(body), "\r\nX-Injected: yes", "must not become a header line")
			assert.NotContains(t, string(body), "\r\n--INJECTED", "must not become a boundary delimiter")
			assert.Contains(t, string(body), "%0D%0A", "CR/LF must be percent-encoded, as mime/multipart does")

			// The receiver sees exactly one part carrying exactly the headers
			// the caller set — the injected ones did not become real headers.
			_, params, err := mime.ParseMediaType(contentType)
			require.NoError(t, err)

			mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
			p, err := mr.NextPart()
			require.NoError(t, err)
			assert.Empty(t, p.Header.Get("X-Injected"))

			_, err = mr.NextPart()
			assert.ErrorIs(t, err, io.EOF, "the forged boundary must not produce a second part")
		})
	}
}

// ContentType is written straight into a header value, where percent-encoding
// would be meaningless — a control character there is rejected instead.
func TestMultipartRequest_RejectsControlCharactersInContentType(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
	}{
		{"CRLF", "application/pdf\r\nX-Injected: yes"},
		{"bare LF", "application/pdf\nX-Injected: yes"},
		{"bare CR", "application/pdf\rX-Injected: yes"},
		{"NUL", "application/pdf\x00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := buildMultipartBody([]Part{
				{Name: "file1", FileName: "invoice.pdf", ContentType: tc.contentType, Content: []byte("x")},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "file1")
			assert.Contains(t, err.Error(), "control character")
		})
	}

	t.Run("an ordinary content type with parameters is still accepted", func(t *testing.T) {
		body, _, err := buildMultipartBody([]Part{
			{Name: "payload", ContentType: "application/json; charset=UTF-8", Content: []byte("{}")},
		})
		require.NoError(t, err)
		assert.Contains(t, string(body), "application/json; charset=UTF-8")
	})
}
