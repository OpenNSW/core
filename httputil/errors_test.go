// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package httputil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var errMemberNotFound = ErrorDef{
	Code:     "PB-1003",
	Category: CategoryNotFound,
	Message:  Message{Key: "pb.member.not_found", DefaultValue: "Member not found"},
	Description: Message{
		Key:          "pb.member.not_found_description",
		DefaultValue: "No member has the ID {{memberId}}",
	},
}

var errInvalidRequest = ErrorDef{
	Code:     "PB-1001",
	Category: CategoryInvalid,
	Message:  Message{Key: "pb.request.invalid", DefaultValue: "Invalid request"},
}

// captureLogs routes the default slog logger to a buffer for the duration of
// the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func writeError(t *testing.T, err error) (*httptest.ResponseRecorder, errorBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	w := httptest.NewRecorder()
	WriteError(w, req, err)
	var body errorBody
	if decodeErr := json.Unmarshal(w.Body.Bytes(), &body); decodeErr != nil {
		t.Fatalf("failed to decode response %s: %v", w.Body, decodeErr)
	}
	return w, body
}

func TestCategory_Status(t *testing.T) {
	tests := []struct {
		category Category
		want     int
	}{
		{CategoryInvalid, http.StatusBadRequest},
		{CategoryUnauthenticated, http.StatusUnauthorized},
		{CategoryForbidden, http.StatusForbidden},
		{CategoryNotFound, http.StatusNotFound},
		{CategoryConflict, http.StatusConflict},
		{CategoryTooLarge, http.StatusRequestEntityTooLarge},
		{CategoryUnsupportedMediaType, http.StatusUnsupportedMediaType},
		{CategoryUnprocessable, http.StatusUnprocessableEntity},
		{CategoryTooManyRequests, http.StatusTooManyRequests},
		{CategoryUnavailable, http.StatusServiceUnavailable},
		{CategoryInternal, http.StatusInternalServerError},
		{NewCategory("tooEarly", http.StatusTooEarly), http.StatusTooEarly},
		{Category{}, http.StatusInternalServerError},
		{NewCategory("ok", http.StatusOK), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		if got := tt.category.Status(); got != tt.want {
			t.Errorf("%q.Status() = %d, want %d", tt.category, got, tt.want)
		}
	}
}

func TestError_WithParamsFillsEachMessageWithItsOwnParams(t *testing.T) {
	err := errMemberNotFound.WithParams(map[string]string{"memberId": "m-42"})

	if err.Message().Params != nil {
		t.Fatalf("expected no params on the title, got %v", err.Message().Params)
	}
	if err.Description().Params["memberId"] != "m-42" {
		t.Fatalf("unexpected description params: %v", err.Description().Params)
	}
	if errMemberNotFound.Description.Params != nil {
		t.Fatal("WithParams modified the declared error")
	}
}

func TestError_WithDetailsDoesNotAlias(t *testing.T) {
	base := errInvalidRequest.WithDetails(FieldError{Field: "a"})
	first := base.WithDetails(FieldError{Field: "b"})
	second := base.WithDetails(FieldError{Field: "c"})

	if len(first.details) != 2 || first.details[1].Field != "b" {
		t.Fatalf("unexpected first details: %+v", first.details)
	}
	if len(second.details) != 2 || second.details[1].Field != "c" {
		t.Fatalf("unexpected second details: %+v", second.details)
	}
}

func TestErrorDef_ReturnsANewErrorEachTime(t *testing.T) {
	if first, second := errMemberNotFound.Err(), errMemberNotFound.Err(); first == second {
		t.Fatal("expected Err to return a new *Error each time")
	}

	var got *Error
	if !errors.As(fmt.Errorf("load: %w", errMemberNotFound.Err()), &got) {
		t.Fatal("expected errors.As to find the *Error")
	}
	_ = got.WithParams(map[string]string{"memberId": "m-42"})

	if errMemberNotFound.Description.Params != nil || got.Description().Params != nil {
		t.Fatal("WithParams on a returned error changed it or its declaration")
	}
}

func TestError_GettersReturnCopies(t *testing.T) {
	err := errMemberNotFound.WithParams(map[string]string{"memberId": "m-42"})

	err.Description().Params["memberId"] = "changed"

	if got := err.Description().Params["memberId"]; got != "m-42" {
		t.Fatalf("changing a getter's params changed the error: %q", got)
	}
}

func TestError_IsAndAs(t *testing.T) {
	cause := errors.New("sql: no rows in result set")
	err := fmt.Errorf("load member: %w",
		errMemberNotFound.WithParams(map[string]string{"memberId": "m-42"}).Wrap(cause))

	if !errors.Is(err, errMemberNotFound.Err()) {
		t.Fatal("expected errors.Is to match the declared error")
	}
	if errors.Is(err, errInvalidRequest.Err()) {
		t.Fatal("expected errors.Is not to match a different error")
	}
	if !errors.Is(err, cause) {
		t.Fatal("expected errors.Is to reach the wrapped cause")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Description().Params["memberId"] != "m-42" {
		t.Fatalf("expected errors.As to find the parameterized error, got %+v", apiErr)
	}
	if !strings.Contains(err.Error(), "PB-1003: Member not found: sql: no rows") {
		t.Fatalf("unexpected error text: %q", err.Error())
	}
}

func TestWriteError_WritesDeclaredErrorWithCategoryStatus(t *testing.T) {
	setCorrelationIDFunc(t, "c0ffee")
	logs := captureLogs(t)

	w, body := writeError(t, errMemberNotFound.WithParams(map[string]string{"memberId": "m-42"}))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	if body.Code != "PB-1003" || body.CorrelationID != "c0ffee" {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.Message.Key != "pb.member.not_found" || body.Message.DefaultValue != "Member not found" {
		t.Fatalf("unexpected message: %+v", body.Message)
	}
	if body.Description.DefaultValue != "No member has the ID m-42" || body.Description.Params["memberId"] != "m-42" {
		t.Fatalf("unexpected description: %+v", body.Description)
	}
	if body.Details == nil || len(body.Details) != 0 {
		t.Fatalf("expected empty details array, got %v", body.Details)
	}
	if logs.Len() != 0 {
		t.Fatalf("expected no log for a plain client error, got %s", logs)
	}
}

func TestWriteError_BodyShape(t *testing.T) {
	setCorrelationIDFunc(t, "c0ffee")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	WriteError(w, req, errMemberNotFound.WithParams(map[string]string{"memberId": "m-42"}))

	want := `{"code":"PB-1003",` +
		`"message":{"key":"pb.member.not_found","defaultValue":"Member not found"},` +
		`"description":{"key":"pb.member.not_found_description","defaultValue":"No member has the ID m-42","params":{"memberId":"m-42"}},` +
		`"details":[],"correlationId":"c0ffee"}` + "\n"
	if got := w.Body.String(); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestWriteError_OmitsEmptyDescriptionAndCorrelationID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	WriteError(w, req, errInvalidRequest.Err())

	got := w.Body.String()
	if strings.Contains(got, "description") || strings.Contains(got, "correlationId") {
		t.Fatalf("expected description and correlationId to be omitted, got %s", got)
	}
}

func TestWriteError_WritesDetails(t *testing.T) {
	err := errInvalidRequest.WithDetails(
		FieldError{
			Field:   "contact.email",
			Code:    "PB-2001",
			Message: Message{Key: "pb.field.email", DefaultValue: "Enter a valid email address"},
		},
		FieldError{
			Field: "name",
			Code:  "PB-2002",
			Message: Message{Key: "pb.field.too_long", DefaultValue: "At most {{max}} characters"}.
				WithParams(map[string]string{"max": "64"}),
		},
	)

	w, body := writeError(t, err)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if len(body.Details) != 2 {
		t.Fatalf("expected 2 details, got %+v", body.Details)
	}
	if d := body.Details[1]; d.Field != "name" || d.Message.DefaultValue != "At most 64 characters" || d.Message.Params["max"] != "64" {
		t.Fatalf("unexpected detail: %+v", d)
	}
}

func TestWriteError_UnknownErrorIsInternalAndNeverLeaks(t *testing.T) {
	setCorrelationIDFunc(t, "corr-abc")
	logs := captureLogs(t)

	w, body := writeError(t, errors.New(`pq: relation "widgets" does not exist`))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	if body.Code != ErrInternal.Code || body.CorrelationID != "corr-abc" {
		t.Fatalf("unexpected body: %+v", body)
	}
	if strings.Contains(w.Body.String(), "pq:") {
		t.Fatalf("response leaked underlying error text: %s", w.Body)
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "pq: relation") ||
		!strings.Contains(logs.String(), "correlationId=corr-abc") {
		t.Fatalf("expected the error to be logged with its correlation ID, got %s", logs)
	}
}

func TestWriteError_NilErrIsInternal(t *testing.T) {
	captureLogs(t)

	w, body := writeError(t, nil)

	if w.Code != http.StatusInternalServerError || body.Code != ErrInternal.Code {
		t.Fatalf("expected ErrInternal, got %d %+v", w.Code, body)
	}
}

func TestWriteError_WrappedCauseIsLoggedNotSent(t *testing.T) {
	logs := captureLogs(t)

	w, body := writeError(t, errMemberNotFound.Wrap(errors.New("sql: no rows in result set")))

	if w.Code != http.StatusNotFound || body.Code != "PB-1003" {
		t.Fatalf("unexpected response: %d %+v", w.Code, body)
	}
	if strings.Contains(w.Body.String(), "sql:") {
		t.Fatalf("response leaked the cause: %s", w.Body)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "sql: no rows") {
		t.Fatalf("expected the cause to be logged at warn level, got %s", logs)
	}
}

func TestWriteError_ServerCategoryLogsAtErrorLevel(t *testing.T) {
	logs := captureLogs(t)
	errUpstream := ErrorDef{
		Code:     "PB-5031",
		Category: CategoryUnavailable,
		Message:  Message{Key: "pb.upstream.unavailable", DefaultValue: "Payment gateway unavailable"},
	}

	w, _ := writeError(t, errUpstream.Wrap(errors.New("dial tcp: connection refused")))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
	if !strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("expected an error-level log, got %s", logs)
	}
}

func TestWriteError_ContextAddedByWrappingIsLogged(t *testing.T) {
	logs := captureLogs(t)

	w, _ := writeError(t, fmt.Errorf("member %s: %w", "m-42", errMemberNotFound.Err()))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if !strings.Contains(logs.String(), "member m-42") {
		t.Fatalf("expected the wrapping context to be logged, got %s", logs)
	}
}
