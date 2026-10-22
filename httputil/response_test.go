// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package httputil

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestCorrelationID_EmptyWhenFuncUnset(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)

	if got := CorrelationID(req); got != "" {
		t.Fatalf("expected empty CorrelationID, got %q", got)
	}
}
