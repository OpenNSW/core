// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package trace

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTraceMiddleware_HeaderExtraction(t *testing.T) {
	tests := []struct {
		name      string
		headers   map[string]string
		wantTrace string
		wantLen   int // if wantTrace is "", assert length instead (fallback)
	}{
		{
			name:      "X-Trace-ID propagated",
			headers:   map[string]string{"X-Trace-ID": "trace-abc"},
			wantTrace: "trace-abc",
		},
		{
			name:      "X-Correlation-ID propagated",
			headers:   map[string]string{"X-Correlation-ID": "corr-xyz"},
			wantTrace: "corr-xyz",
		},
		{
			name:      "X-Request-ID propagated",
			headers:   map[string]string{"X-Request-ID": "req-123"},
			wantTrace: "req-123",
		},
		{
			name: "X-Trace-ID takes precedence over X-Correlation-ID",
			headers: map[string]string{
				"X-Trace-ID":       "trace-wins",
				"X-Correlation-ID": "corr-loses",
			},
			wantTrace: "trace-wins",
		},
		{
			name: "X-Trace-ID takes precedence over X-Request-ID",
			headers: map[string]string{
				"X-Trace-ID":   "trace-wins",
				"X-Request-ID": "req-loses",
			},
			wantTrace: "trace-wins",
		},
		{
			name: "X-Correlation-ID takes precedence over X-Request-ID",
			headers: map[string]string{
				"X-Correlation-ID": "corr-wins",
				"X-Request-ID":     "req-loses",
			},
			wantTrace: "corr-wins",
		},
		{
			name:    "fallback generates 32-char hex trace ID",
			headers: nil,
			wantLen: 32,
		},
		{
			name:    "invalid trace ID discarded (newline control character)",
			headers: map[string]string{"X-Trace-ID": "trace\nnewline"},
			wantLen: 32, // falls back to generated
		},
		{
			name:    "invalid trace ID discarded (carriage return control character)",
			headers: map[string]string{"X-Trace-ID": "trace\rreturn"},
			wantLen: 32, // falls back to generated
		},
		{
			name:    "invalid trace ID discarded (too long)",
			headers: map[string]string{"X-Trace-ID": strings.Repeat("a", 65)},
			wantLen: 32, // falls back to generated
		},
		{
			name:      "valid trace ID with special characters accepted",
			headers:   map[string]string{"X-Trace-ID": "trace-abc_123:./="},
			wantTrace: "trace-abc_123:./=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedTraceID string
			handler := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedTraceID = GetTraceID(r.Context())
				if capturedTraceID == "" {
					t.Error("expected trace ID in context, got empty")
				}
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/api/v1/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if tt.wantTrace != "" {
				if capturedTraceID != tt.wantTrace {
					t.Errorf("context trace ID = %q, want %q", capturedTraceID, tt.wantTrace)
				}
			} else if tt.wantLen > 0 {
				if len(capturedTraceID) != tt.wantLen {
					t.Errorf("fallback trace ID length = %d, want %d (value: %q)", len(capturedTraceID), tt.wantLen, capturedTraceID)
				}
			}

			respHeader := w.Header().Get("X-Trace-ID")
			if respHeader != capturedTraceID {
				t.Errorf("response X-Trace-ID = %q, want %q", respHeader, capturedTraceID)
			}
		})
	}
}

func TestContextWithTraceID(t *testing.T) {
	ctx := ContextWithTraceID(t.Context(), "injected-id")
	got := GetTraceID(ctx)
	if got != "injected-id" {
		t.Errorf("GetTraceID() = %q, want %q", got, "injected-id")
	}
}

func TestGetTraceID_Empty(t *testing.T) {
	got := GetTraceID(t.Context())
	if got != "" {
		t.Errorf("GetTraceID() on bare context = %q, want empty", got)
	}
}
