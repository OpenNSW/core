// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package httputil provides shared HTTP response helpers for writing JSON
// payloads and translatable, correlation-ID-tagged API error bodies.
package httputil

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// CorrelationIDFunc optionally extracts a correlation ID from a request
// context for inclusion in WriteError responses. It is nil by default, in
// which case CorrelationID returns "". A consumer that wants correlation IDs
// wires in an extractor of its choosing, e.g.:
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
// correlationId field of WriteError responses.
func CorrelationID(r *http.Request) string {
	if CorrelationIDFunc == nil {
		return ""
	}
	return CorrelationIDFunc(r.Context())
}
