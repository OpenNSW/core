// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package httputil

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type decodeTarget struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func decodeBody(t *testing.T, body string, limit int64) (decodeTarget, error) {
	t.Helper()
	var dst decodeTarget
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	err := DecodeJSON(httptest.NewRecorder(), r, limit, &dst)
	return dst, err
}

func TestDecodeJSON_DecodesOneValue(t *testing.T) {
	dst, err := decodeBody(t, `{"name":"a","count":2}`, 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst.Name != "a" || dst.Count != 2 {
		t.Fatalf("unexpected value: %+v", dst)
	}
}

func TestDecodeJSON_AcceptsTrailingWhitespace(t *testing.T) {
	if _, err := decodeBody(t, "{\"name\":\"a\"}\n \t\n", 1024); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeJSON_AcceptsBodyOfExactlyTheLimit(t *testing.T) {
	body := `{"name":"abc"}`
	if _, err := decodeBody(t, body, int64(len(body))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeJSON_Errors(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		limit int64
		want  error
	}{
		{"one byte over the limit", `{"name":"abc"}`, int64(len(`{"name":"abc"}`)) - 1, ErrBodyTooLarge},
		{"trailing value over the limit", `{"name":"a"} {"name":"bbbbbbbbbbbbbbbb"}`, 20, ErrBodyTooLarge},
		{"empty", ``, 1024, ErrBodyEmpty},
		{"whitespace only", " \n\t", 1024, ErrBodyEmpty},
		{"malformed", `{"name":`, 1024, ErrBodyInvalid},
		{"wrong type", `{"count":"two"}`, 1024, ErrBodyInvalid},
		{"unknown field", `{"name":"a","extra":1}`, 1024, ErrBodyInvalid},
		{"second value", `{"name":"a"}{"name":"b"}`, 1024, ErrBodyInvalid},
		{"trailing garbage", `{"name":"a"} x`, 1024, ErrBodyInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeBody(t, tt.body, tt.limit)
			if !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}
}

func TestDecodeJSON_NamesUnknownField(t *testing.T) {
	_, err := decodeBody(t, `{"name":"a","extra":1}`, 1024)

	var unknown *UnknownFieldError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected *UnknownFieldError, got %T: %v", err, err)
	}
	if unknown.Field != "extra" {
		t.Fatalf("expected field %q, got %q", "extra", unknown.Field)
	}
}

func TestDecodeJSON_UnknownFieldInNestedObject(t *testing.T) {
	var dst struct {
		Inner decodeTarget `json:"inner"`
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"inner":{"nope":true}}`))

	err := DecodeJSON(httptest.NewRecorder(), r, 1024, &dst)

	var unknown *UnknownFieldError
	if !errors.As(err, &unknown) || unknown.Field != "nope" {
		t.Fatalf("expected unknown field %q, got %v", "nope", err)
	}
}

func TestDecodeJSON_FreeFormValuesTakeAnyKey(t *testing.T) {
	var dst struct {
		Payload map[string]any `json:"payload"`
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"payload":{"anything":1}}`))

	if err := DecodeJSON(httptest.NewRecorder(), r, 1024, &dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst.Payload["anything"] != float64(1) {
		t.Fatalf("unexpected payload: %+v", dst.Payload)
	}
}

func TestWriteDecodeError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
	}{
		{"too large", ErrBodyTooLarge, http.StatusRequestEntityTooLarge, MsgBodyTooLarge},
		{"empty", ErrBodyEmpty, http.StatusBadRequest, MsgInvalidBody},
		{"invalid", ErrBodyInvalid, http.StatusBadRequest, MsgInvalidBody},
		{"unknown field", &UnknownFieldError{Field: "extra"}, http.StatusBadRequest, `invalid request body: unknown field "extra"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setCorrelationIDFunc(t, "corr-1")
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", nil)

			WriteDecodeError(w, r, tt.err)

			if w.Code != tt.wantStatus {
				t.Fatalf("expected %d, got %d", tt.wantStatus, w.Code)
			}
			var got ErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if got.Error != tt.wantError {
				t.Fatalf("expected error %q, got %q", tt.wantError, got.Error)
			}
			if got.CorrelationID != "corr-1" {
				t.Fatalf("expected correlationId corr-1, got %q", got.CorrelationID)
			}
		})
	}
}

func TestWriteDecodeError_AnswersDecodeJSONErrors(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"abcdef"}`))
	w := httptest.NewRecorder()
	err := DecodeJSON(w, r, 4, &decodeTarget{})

	WriteDecodeError(w, r, err)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", w.Code)
	}
}
