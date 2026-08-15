// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// quoteEscaper escapes the two characters that would otherwise break out of a
// quoted Content-Disposition parameter. It matches what mime/multipart does
// internally for CreateFormFile, which this package cannot reuse because that
// helper hardcodes a Content-Type of application/octet-stream.
var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

// Part is one part of a multipart/form-data body.
//
// The zero FileName sends a plain form field; a non-empty FileName sends the
// part as an uploaded file, adding filename="..." to Content-Disposition.
// ContentType is written as the part's Content-Type header when set, and
// omitted otherwise — some receivers distinguish a JSON part from a text field
// by that header alone.
type Part struct {
	Name        string // form field name; required
	FileName    string // optional; set to send the part as a file
	ContentType string // optional; e.g. "application/json", "application/pdf"
	Content     []byte
}

// JSONPart marshals v and returns a Part carrying it as application/json.
// It saves callers hand-marshalling the one part that is rarely a plain
// string, and keeps the Content-Type spelling consistent across services.
func JSONPart(name string, v any) (Part, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return Part{}, fmt.Errorf("remote: failed to marshal multipart part %q: %w", name, err)
	}
	return Part{Name: name, ContentType: "application/json; charset=UTF-8", Content: data}, nil
}

// MultipartRequest bundles the caller-provided parts of an outbound
// multipart/form-data call. The Content-Type header (including the generated
// boundary) is set by the client and must not be supplied in Headers.
type MultipartRequest struct {
	Method  string
	Path    string
	Query   url.Values
	Parts   []Part
	Headers map[string]string
	Retry   *RetryConfig // If nil, no retries will be performed
}

// MultipartRequest sends req.Parts as a multipart/form-data body and decodes a
// JSON response into response (pass nil to discard it).
//
// Error semantics match JSONRequest rather than RawRequest: the body is
// decoded first and a non-2xx status is then returned as an error, so a caller
// that passes a response still sees the service's error payload alongside the
// error. Parts are buffered in memory, which keeps the body replayable across
// retries — size uploads with that in mind.
func (c *Client) MultipartRequest(ctx context.Context, req MultipartRequest, response any) error {
	if len(req.Parts) == 0 {
		return fmt.Errorf("remote: multipart request requires at least one part")
	}

	body, contentType, err := buildMultipartBody(req.Parts)
	if err != nil {
		return err
	}

	fullPath := req.Path
	if len(req.Query) > 0 {
		if strings.Contains(req.Path, "?") {
			fullPath += "&" + req.Query.Encode()
		} else {
			fullPath += "?" + req.Query.Encode()
		}
	}

	// The generated boundary is part of the Content-Type, so it is set here
	// rather than left to the caller; an override would desync the two and the
	// receiver would fail to parse a body it cannot find the boundary for.
	headers := make(map[string]string, len(req.Headers)+1)
	for k, v := range req.Headers {
		headers[k] = v
	}
	headers["Content-Type"] = contentType

	// The body is already a []byte, so go straight to executeWithRetry rather
	// than through Do, which would wrap it in a reader only to io.ReadAll it
	// back out.
	resp, err := c.executeWithRetry(ctx, req.Method, fullPath, body, headers, req.Retry)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.logger.WarnContext(ctx, "remote: failed to close response body", "error", err)
		}
	}()

	if response != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
			return fmt.Errorf("remote: failed to decode response: %w", err)
		}
	}

	if resp.StatusCode >= 400 {
		return c.handleErrorResponse(resp)
	}

	return nil
}

// buildMultipartBody writes parts into a multipart body and returns it with
// the matching Content-Type (which carries the generated boundary).
func buildMultipartBody(parts []Part) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	for i, p := range parts {
		if p.Name == "" {
			return nil, "", fmt.Errorf("remote: multipart part %d has no name", i)
		}

		disposition := fmt.Sprintf(`form-data; name="%s"`, quoteEscaper.Replace(p.Name))
		if p.FileName != "" {
			disposition += fmt.Sprintf(`; filename="%s"`, quoteEscaper.Replace(p.FileName))
		}

		header := make(textproto.MIMEHeader, 2)
		header.Set("Content-Disposition", disposition)
		if p.ContentType != "" {
			header.Set("Content-Type", p.ContentType)
		}

		pw, err := w.CreatePart(header)
		if err != nil {
			return nil, "", fmt.Errorf("remote: failed to create multipart part %q: %w", p.Name, err)
		}
		if _, err := io.Copy(pw, bytes.NewReader(p.Content)); err != nil {
			return nil, "", fmt.Errorf("remote: failed to write multipart part %q: %w", p.Name, err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("remote: failed to finalize multipart body: %w", err)
	}

	return buf.Bytes(), w.FormDataContentType(), nil
}
