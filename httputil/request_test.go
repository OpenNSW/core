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

// Whatever follows the first value is refused as before, and the error says the value
// decoded, so the destination holds it.
func TestDecodeJSON_TrailingDataKeepsTheValue(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		limit int64
		want  error
	}{
		{"second value", `{"name":"a"}{"name":"b"}`, 1024, ErrBodyInvalid},
		{"trailing garbage", `{"name":"a"} x`, 1024, ErrBodyInvalid},
		{"trailing value over the limit", `{"name":"a"} {"name":"bbbbbbbbbbbbbbbb"}`, 20, ErrBodyTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst, err := decodeBody(t, tt.body, tt.limit)
			if !errors.Is(err, ErrTrailingData) || !errors.Is(err, tt.want) {
				t.Fatalf("expected ErrTrailingData and %v, got %v", tt.want, err)
			}
			if dst.Name != "a" {
				t.Fatalf("expected the first value to be decoded, got %+v", dst)
			}
		})
	}
}

// An error in or before the first value is not ErrTrailingData: nothing decoded.
func TestDecodeJSON_ErrorsBeforeTheValueAreNotTrailingData(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		limit int64
	}{
		{"value over the limit", `{"name":"abc"}`, int64(len(`{"name":"abc"}`)) - 1},
		{"empty", ``, 1024},
		{"malformed", `{"name":`, 1024},
		{"wrong type", `{"count":"two"}`, 1024},
		{"unknown field", `{"name":"a","extra":1}`, 1024},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeBody(t, tt.body, tt.limit)
			if err == nil || errors.Is(err, ErrTrailingData) {
				t.Fatalf("expected an error that is not ErrTrailingData, got %v", err)
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

func TestDecodeJSON_AllowUnknownFields(t *testing.T) {
	var dst decodeTarget
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"a","extra":1}`))

	if err := DecodeJSON(httptest.NewRecorder(), r, 1024, &dst, AllowUnknownFields()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst.Name != "a" {
		t.Fatalf("unexpected value: %+v", dst)
	}
}

func TestDecodeJSON_AllowTrailingData(t *testing.T) {
	for _, body := range []string{`{"name":"a"}{"name":"b"}`, `{"name":"a"} x`} {
		var dst decodeTarget
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))

		if err := DecodeJSON(httptest.NewRecorder(), r, 1024, &dst, AllowTrailingData()); err != nil {
			t.Fatalf("%s: unexpected error: %v", body, err)
		}
		if dst.Name != "a" {
			t.Fatalf("%s: unexpected value: %+v", body, dst)
		}
	}
}

// Each option relaxes only its own rule; the others, and the limit, still apply.
func TestDecodeJSON_OptionsRelaxOnlyTheirOwnRule(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		limit int64
		opt   DecodeOption
		want  error
	}{
		{"unknown fields allowed, trailing data refused", `{"name":"a","extra":1}{}`, 1024, AllowUnknownFields(), ErrBodyInvalid},
		{"trailing data allowed, unknown field refused", `{"name":"a","extra":1}`, 1024, AllowTrailingData(), ErrBodyInvalid},
		{"unknown fields allowed, over the limit", `{"name":"abcdef","extra":1}`, 8, AllowUnknownFields(), ErrBodyTooLarge},
		{"trailing data allowed, empty", ``, 1024, AllowTrailingData(), ErrBodyEmpty},
		{"trailing data allowed, malformed", `{"name":`, 1024, AllowTrailingData(), ErrBodyInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			err := DecodeJSON(httptest.NewRecorder(), r, tt.limit, &decodeTarget{}, tt.opt)
			if !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
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
	tests := []struct {
		name  string
		body  string
		limit int64
		want  int
	}{
		{"value over the limit", `{"name":"abcdef"}`, 4, http.StatusRequestEntityTooLarge},
		{"trailing value over the limit", `{"name":"a"} {"name":"bbbbbbbbbbbbbbbb"}`, 20, http.StatusRequestEntityTooLarge},
		{"second value", `{"name":"a"}{"name":"b"}`, 1024, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			err := DecodeJSON(w, r, tt.limit, &decodeTarget{})

			WriteDecodeError(w, r, err)

			if w.Code != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, w.Code)
			}
		})
	}
}
