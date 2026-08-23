// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// Body encodes a request payload into wire bytes and the Content-Type header
// that describes them. The returned Content-Type always wins over the same
// key in Request.Headers — the encoding (e.g. a multipart boundary) is only
// valid paired with the Content-Type that names it.
type Body interface {
	Encode() (data []byte, contentType string, err error)
}

// JSONBody marshals V as a JSON request body.
type JSONBody struct {
	V any
}

func (b JSONBody) Encode() ([]byte, string, error) {
	data, err := json.Marshal(b.V)
	if err != nil {
		return nil, "", fmt.Errorf("remote: failed to marshal JSON body: %w", err)
	}
	return data, "application/json", nil
}

// RawBody sends Data verbatim under ContentType — e.g. a SOAP/XML envelope.
// An empty Data sends no body and sets no Content-Type.
type RawBody struct {
	Data        []byte
	ContentType string
}

func (b RawBody) Encode() ([]byte, string, error) {
	if len(b.Data) == 0 {
		return nil, "", nil
	}
	return b.Data, b.ContentType, nil
}

// FormBody encodes Values as application/x-www-form-urlencoded.
type FormBody struct {
	Values url.Values
}

func (b FormBody) Encode() ([]byte, string, error) {
	return []byte(b.Values.Encode()), "application/x-www-form-urlencoded", nil
}

// MultipartBody encodes Parts as a multipart/form-data request body. Encode
// generates a fresh boundary on every call, so its Content-Type return must
// be used verbatim — it cannot be precomputed or cached. Parts are buffered
// in memory, which keeps the body replayable across retries — size uploads
// with that in mind. See multipart.go for Part and the encoding itself.
type MultipartBody struct {
	Parts []Part
}

func (b MultipartBody) Encode() ([]byte, string, error) {
	if len(b.Parts) == 0 {
		return nil, "", fmt.Errorf("remote: multipart body requires at least one part")
	}
	return buildMultipartBody(b.Parts)
}
