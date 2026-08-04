// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package httputil

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setCorrelationIDFunc wires CorrelationIDFunc to return id for the duration
// of the test, restoring the previous value on cleanup.
func setCorrelationIDFunc(t *testing.T, id string) {
	t.Helper()
	prev := CorrelationIDFunc
	CorrelationIDFunc = func(context.Context) string { return id }
	t.Cleanup(func() { CorrelationIDFunc = prev })
}

func TestJSON_WritesStatusContentTypeAndBody(t *testing.T) {
	w := httptest.NewRecorder()

	JSON(w, http.StatusCreated, map[string]string{"id": "c-1"})

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	var got map[string]string
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got["id"] != "c-1" {
		t.Fatalf("unexpected body: %+v", got)
	}
}

type errorWriter struct{ header http.Header }

func (e *errorWriter) Header() http.Header       { return e.header }
func (e *errorWriter) Write([]byte) (int, error) { return 0, errors.New("write error") }
func (e *errorWriter) WriteHeader(int)           {}

func TestJSON_WriteFailureDoesNotPanic(t *testing.T) {
	JSON(&errorWriter{header: http.Header{}}, http.StatusOK, map[string]string{"id": "c-1"})
}

func TestJSON_EncodeFailureReturns500(t *testing.T) {
	w := httptest.NewRecorder()

	JSON(w, http.StatusCreated, make(chan int))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestCorrelationID_MatchesResponseCorrelationID(t *testing.T) {
	setCorrelationIDFunc(t, "corr-xyz")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)

	if got := CorrelationID(req); got != "corr-xyz" {
		t.Fatalf("expected CorrelationID %q, got %q", "corr-xyz", got)
	}

	// CorrelationID must return the same value CorrelationIDFunc produces as
	// the one Error/InternalServerError send back as correlationId —
	// otherwise a client-reported correlationId isn't useful for lookups
	// against whatever the consumer wired CorrelationIDFunc to.
	w := httptest.NewRecorder()
	Error(w, req, http.StatusNotFound, "resource not found")
	var got ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.CorrelationID != CorrelationID(req) {
		t.Fatalf("correlationId %q does not match CorrelationID(r) %q", got.CorrelationID, CorrelationID(req))
	}
}

func TestCorrelationID_EmptyWhenFuncUnset(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)

	if got := CorrelationID(req); got != "" {
		t.Fatalf("expected empty CorrelationID, got %q", got)
	}
}

func TestError_WritesMessageAndCorrelationID(t *testing.T) {
	setCorrelationIDFunc(t, "corr-123")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	w := httptest.NewRecorder()

	Error(w, req, http.StatusNotFound, "resource not found")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	var got ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.Error != "resource not found" {
		t.Fatalf("expected message %q, got %q", "resource not found", got.Error)
	}
	if got.CorrelationID != "corr-123" {
		t.Fatalf("expected correlationId %q, got %q", "corr-123", got.CorrelationID)
	}
}

func TestError_NoCorrelationIDFuncOmitsCorrelationID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	w := httptest.NewRecorder()

	Error(w, req, http.StatusBadRequest, "invalid pagination parameters")

	body := w.Body.String()
	if strings.Contains(body, "correlationId") {
		t.Fatalf("expected correlationId to be omitted when CorrelationIDFunc is unset, got body: %s", body)
	}
	var got ErrorResponse
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.Error != "invalid pagination parameters" {
		t.Fatalf("unexpected error message: %q", got.Error)
	}
}

func TestInternalServerError_NeverExposesUnderlyingErrorText(t *testing.T) {
	setCorrelationIDFunc(t, "corr-abc")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	w := httptest.NewRecorder()

	sensitiveErr := errors.New("pq: relation \"widgets\" does not exist")
	InternalServerError(w, req, "failed to retrieve resource", sensitiveErr)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	var got ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if strings.Contains(got.Error, "pq:") || strings.Contains(got.Error, "relation") {
		t.Fatalf("response leaked underlying error text: %q", got.Error)
	}
	if got.Error != "An error occurred while processing your request" {
		t.Fatalf("unexpected generic message: %q", got.Error)
	}
	if got.CorrelationID != "corr-abc" {
		t.Fatalf("expected correlationId %q, got %q", "corr-abc", got.CorrelationID)
	}
}

func TestInternalServerError_NilErrDoesNotPanic(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	w := httptest.NewRecorder()

	InternalServerError(w, req, "resource is nil after successful creation", nil)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestInternalServerError_WithAttrsDoesNotPanic(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	w := httptest.NewRecorder()

	InternalServerError(w, req, "failed to resolve user", errors.New("boom"), "key", "value")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}
