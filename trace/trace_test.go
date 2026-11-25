// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package trace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestTraceMiddleware_ServerGeneratedIgnoresClientHeaders(t *testing.T) {
	tests := []struct {
		name            string
		headers         map[string]string
		wantClientID    string
		wantClientEmpty bool
	}{
		{
			name:         "X-Trace-ID kept as clientRequestId only",
			headers:      map[string]string{"X-Trace-ID": "trace-abc"},
			wantClientID: "trace-abc",
		},
		{
			name:         "X-Correlation-ID kept as clientRequestId only",
			headers:      map[string]string{"X-Correlation-ID": "corr-xyz"},
			wantClientID: "corr-xyz",
		},
		{
			name:         "X-Request-ID kept as clientRequestId only",
			headers:      map[string]string{"X-Request-ID": "req-123"},
			wantClientID: "req-123",
		},
		{
			name: "X-Trace-ID takes precedence for clientRequestId",
			headers: map[string]string{
				"X-Trace-ID":       "trace-wins",
				"X-Correlation-ID": "corr-loses",
			},
			wantClientID: "trace-wins",
		},
		{
			name:            "no client headers",
			headers:         nil,
			wantClientEmpty: true,
		},
		{
			name:            "invalid client id discarded (newline)",
			headers:         map[string]string{"X-Trace-ID": "trace\nnewline"},
			wantClientEmpty: true,
		},
		{
			name:            "invalid client id discarded (too long)",
			headers:         map[string]string{"X-Trace-ID": strings.Repeat("a", 65)},
			wantClientEmpty: true,
		},
		{
			name:         "valid client id with special characters accepted",
			headers:      map[string]string{"X-Trace-ID": "trace-abc_123:./="},
			wantClientID: "trace-abc_123:./=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedTraceID, capturedClientID string
			handler := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedTraceID = GetTraceID(r.Context())
				capturedClientID = GetClientRequestID(r.Context())
				if capturedTraceID == "" {
					t.Error("expected server trace ID in context, got empty")
				}
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/api/v1/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if len(capturedTraceID) != 32 {
				t.Errorf("server trace ID length = %d, want 32 (value: %q)", len(capturedTraceID), capturedTraceID)
			}
			if tt.wantClientID != "" && capturedClientID != tt.wantClientID {
				t.Errorf("clientRequestId = %q, want %q", capturedClientID, tt.wantClientID)
			}
			if tt.wantClientEmpty && capturedClientID != "" {
				t.Errorf("clientRequestId = %q, want empty", capturedClientID)
			}
			if tt.wantClientID != "" && capturedTraceID == tt.wantClientID {
				t.Errorf("server trace ID must not equal client header %q", tt.wantClientID)
			}
			if resp := w.Header().Get("X-Trace-ID"); resp != capturedTraceID {
				t.Errorf("response X-Trace-ID = %q, want %q", resp, capturedTraceID)
			}
			if tt.wantClientID != "" && w.Header().Get("X-Trace-ID") == tt.wantClientID {
				t.Errorf("response X-Trace-ID must not echo client header %q", tt.wantClientID)
			}
		})
	}
}

func TestTraceMiddleware_UsesActiveSpanTraceID(t *testing.T) {
	const want = "ba06a9e9121683fe4b08a16cc00a3f59"
	tid, err := oteltrace.TraceIDFromHex(want)
	if err != nil {
		t.Fatal(err)
	}
	var sid oteltrace.SpanID
	sid[7] = 1
	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: oteltrace.FlagsSampled,
	})
	ctx := oteltrace.ContextWithSpanContext(t.Context(), sc)

	var captured, capturedClient string
	handler := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = GetTraceID(r.Context())
		capturedClient = GetClientRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequestWithContext(ctx, "GET", "/api/v1/test", nil)
	req.Header.Set("X-Trace-ID", "client-must-not-win")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if captured != want {
		t.Fatalf("GetTraceID = %q, want span TraceID %q", captured, want)
	}
	if capturedClient != "client-must-not-win" {
		t.Fatalf("GetClientRequestID = %q, want client-must-not-win", capturedClient)
	}
	if w.Header().Get("X-Trace-ID") != want {
		t.Fatalf("response X-Trace-ID = %q, want %q", w.Header().Get("X-Trace-ID"), want)
	}
}

func TestTraceSlotMiddleware_OuterGetTraceIDSeesFilledSlot(t *testing.T) {
	var outerTraceID, outerClientID, innerTraceID string
	inner := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerTraceID = GetTraceID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	outer := TraceSlotMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r)
		outerTraceID = GetTraceID(r.Context())
		outerClientID = GetClientRequestID(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "from-client")
	w := httptest.NewRecorder()
	outer.ServeHTTP(w, req)

	if innerTraceID == "" || len(innerTraceID) != 32 {
		t.Fatalf("inner trace ID = %q", innerTraceID)
	}
	if outerTraceID != innerTraceID {
		t.Fatalf("outer GetTraceID = %q, want inner %q", outerTraceID, innerTraceID)
	}
	if outerClientID != "from-client" {
		t.Fatalf("outer clientRequestId = %q, want from-client", outerClientID)
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

func TestContextWithClientRequestID(t *testing.T) {
	ctx := ContextWithClientRequestID(context.Background(), "client-1")
	if got := GetClientRequestID(ctx); got != "client-1" {
		t.Errorf("GetClientRequestID() = %q, want client-1", got)
	}
}


func TestTraceMiddleware_PropagatesPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	var outerPattern string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		TraceMiddleware(mux).ServeHTTP(w, r)
		outerPattern = r.Pattern
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if outerPattern != "GET /api/v1/tasks" {
		t.Fatalf("outer Pattern = %q, want GET /api/v1/tasks", outerPattern)
	}
}
