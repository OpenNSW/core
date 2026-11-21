// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package httputil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Client-facing messages for a request body DecodeJSON refuses. WriteDecodeError sends
// them; a handler that answers a decode error itself, for example to audit it, sends the
// same ones so every endpoint describes a bad body alike.
const (
	MsgInvalidBody  = "invalid request body"
	MsgBodyTooLarge = "request body too large"
)

var (
	// ErrBodyTooLarge is returned by DecodeJSON for a body longer than its limit.
	ErrBodyTooLarge = errors.New("httputil: request body too large")
	// ErrBodyEmpty is returned by DecodeJSON for a body that holds no JSON value. A
	// handler whose body is optional can accept it.
	ErrBodyEmpty = errors.New("httputil: request body is empty")
	// ErrBodyInvalid is returned by DecodeJSON for a body that is not exactly one JSON
	// value of the destination's shape: malformed JSON, a value of the wrong type, a
	// field the destination does not declare (an *UnknownFieldError), or anything after
	// the value.
	ErrBodyInvalid = errors.New("httputil: invalid request body")
)

// UnknownFieldError is the ErrBodyInvalid DecodeJSON returns for an object field the
// destination does not declare. Naming the field tells the caller what to fix.
type UnknownFieldError struct {
	Field string
}

func (e *UnknownFieldError) Error() string {
	return fmt.Sprintf("httputil: unknown field %q", e.Field)
}

// Is reports the error as ErrBodyInvalid.
func (e *UnknownFieldError) Is(target error) bool {
	return target == ErrBodyInvalid
}

// DecodeJSON reads r's body as exactly one JSON value into dst. limit is the most bytes
// the body may hold and must be positive; a longer body is refused with ErrBodyTooLarge,
// and http.MaxBytesReader closes the connection after the response. A field dst does not
// declare, or anything after the value, is refused with ErrBodyInvalid. An empty body is
// ErrBodyEmpty.
//
// DecodeJSON writes nothing to w: the caller answers the error, with WriteDecodeError or
// its own response.
func DecodeJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return ErrBodyEmpty
		}
		return decodeError(err)
	}
	// Decode reads only the first value, so a second one confirms nothing follows it.
	err := dec.Decode(new(json.RawMessage))
	switch {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return fmt.Errorf("%w: data after the JSON value", ErrBodyInvalid)
	default:
		return decodeError(err)
	}
}

// decodeError classifies an error from decoding a request body.
func decodeError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return fmt.Errorf("%w: limit is %d bytes", ErrBodyTooLarge, tooLarge.Limit)
	}
	// encoding/json has no typed error for an unknown field; this is the message it
	// reports one with.
	if quoted, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		if field, unquoteErr := strconv.Unquote(quoted); unquoteErr == nil {
			return &UnknownFieldError{Field: field}
		}
	}
	return fmt.Errorf("%w: %w", ErrBodyInvalid, err)
}

// WriteDecodeError responds to an error from DecodeJSON: 413 with MsgBodyTooLarge for a
// body over the limit, otherwise 400 with MsgInvalidBody, naming the field when it is one
// the destination does not declare.
func WriteDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrBodyTooLarge) {
		Error(w, r, http.StatusRequestEntityTooLarge, MsgBodyTooLarge)
		return
	}
	var unknown *UnknownFieldError
	if errors.As(err, &unknown) {
		Error(w, r, http.StatusBadRequest, fmt.Sprintf("%s: unknown field %q", MsgInvalidBody, unknown.Field))
		return
	}
	Error(w, r, http.StatusBadRequest, MsgInvalidBody)
}
