// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"strings"
)

// quoteEscaper neutralises the characters that would otherwise break out of a
// quoted Content-Disposition parameter: a quote or backslash ends the quoted
// string, and a CR or LF ends the header line itself, letting whatever follows
// it be read as further headers or as a forged part. Callers routinely put
// untrusted input in these fields — an end user's original upload filename is
// the usual case — so escaping here is load-bearing, not cosmetic.
//
// This is only needed for a plain field's `name`-only disposition:
// multipart.FileContentDisposition does the same escaping for the filename
// case, but always appends a filename parameter, so it can't be used here.
var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"", "\r", "%0D", "\n", "%0A")

// hasControlChars reports whether v contains a character that must never reach
// a MIME header line.
//
// mime/multipart writes header values verbatim, so this is what stops an
// unescaped value from terminating its line early. It is for values that are
// not quoted parameters, where percent-encoding would corrupt a legitimate
// value rather than protect anything and rejection is the only honest option.
func hasControlChars(v string) bool {
	return strings.IndexFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}

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

// buildMultipartBody writes parts into a multipart body and returns it with
// the matching Content-Type (which carries the generated boundary).
func buildMultipartBody(parts []Part) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	for i, p := range parts {
		if p.Name == "" {
			return nil, "", fmt.Errorf("remote: multipart part %d has no name", i)
		}
		// Name and FileName are quoted parameters, escaped below (via
		// quoteEscaper or multipart.FileContentDisposition). ContentType is
		// written as a bare header value with nowhere to escape to, so it has
		// to be refused instead.
		if hasControlChars(p.ContentType) {
			return nil, "", fmt.Errorf("remote: multipart part %q has a content type containing a control character", p.Name)
		}

		var disposition string
		if p.FileName != "" {
			disposition = multipart.FileContentDisposition(p.Name, p.FileName)
		} else {
			disposition = fmt.Sprintf(`form-data; name="%s"`, quoteEscaper.Replace(p.Name))
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
