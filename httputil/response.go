// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package httputil provides shared HTTP response helpers for writing JSON
// payloads and standardized, correlation-ID-tagged API error bodies.
package httputil

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// ErrorResponse is the standard JSON shape for API error bodies.
type ErrorResponse struct {
	Error         string `json:"error"`
	CorrelationID string `json:"correlationId,omitempty"`
}

// CorrelationIDFunc optionally extracts a correlation ID from a request
// context for inclusion in Error/InternalServerError responses. It is nil by
// default, in which case CorrelationID returns "". A consumer that wants
// correlation IDs wires in an extractor of its choosing, e.g.:
//
//	httputil.CorrelationIDFunc = trace.GetTraceID
var CorrelationIDFunc func(context.Context) string

// JSON writes payload as the JSON response body with the given status. If
// payload fails to encode, it responds with 500 instead of a partial body
// under the originally requested status.
func JSON(w http.ResponseWriter, status int, payload any) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		slog.Error("httputil: failed to encode JSON response", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body.Bytes()); err != nil {
		slog.Error("httputil: failed to write JSON response", "error", err)
	}
}

// CorrelationID returns the request's correlation ID via CorrelationIDFunc,
// or "" if CorrelationIDFunc is unset. This is the same value included in the
// correlationId field of Error/InternalServerError responses.
func CorrelationID(r *http.Request) string {
	if CorrelationIDFunc == nil {
		return ""
	}
	return CorrelationIDFunc(r.Context())
}

// Error responds with a fixed, safe message for an expected client-facing condition.
func Error(w http.ResponseWriter, r *http.Request, status int, message string) {
	JSON(w, status, ErrorResponse{Error: message, CorrelationID: CorrelationID(r)})
}

// InternalServerError logs err server-side and responds with a generic, safe message to the client.
func InternalServerError(w http.ResponseWriter, r *http.Request, logMessage string, err error, attrs ...any) {
	correlationID := CorrelationID(r)
	args := append([]any{"error", err}, attrs...)
	slog.ErrorContext(r.Context(), logMessage, args...)
	JSON(w, http.StatusInternalServerError, ErrorResponse{
		Error:         "An error occurred while processing your request",
		CorrelationID: correlationID,
	})
}
