// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/OpenNSW/core/trace"
)

func TestHandler_Handle(t *testing.T) {
	tests := []struct {
		name        string
		withTraceID bool
		traceID     string
	}{
		{
			name:        "attaches traceId when context carries one",
			withTraceID: true,
			traceID:     "trace-abc-123",
		},
		{
			name:        "omits traceId when context has none",
			withTraceID: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			base := slog.NewJSONHandler(&buf, nil)
			handler := NewHandler(base)
			logger := slog.New(handler)

			ctx := t.Context()
			if tt.withTraceID {
				ctx = trace.ContextWithTraceID(ctx, tt.traceID)
			}

			logger.InfoContext(ctx, "test message")

			var record map[string]any
			if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
				t.Fatalf("failed to unmarshal log record: %v", err)
			}

			gotTraceID, hasTraceID := record["traceId"]
			if tt.withTraceID {
				if !hasTraceID {
					t.Fatalf("expected traceId attribute, got none: %v", record)
				}
				if gotTraceID != tt.traceID {
					t.Errorf("traceId = %v, want %q", gotTraceID, tt.traceID)
				}
			} else if hasTraceID {
				t.Errorf("expected no traceId attribute, got %v", gotTraceID)
			}
		})
	}
}

func TestHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	handler := NewHandler(base)

	wrapped := handler.WithAttrs([]slog.Attr{slog.String("service", "core")})

	if _, ok := wrapped.(*Handler); !ok {
		t.Fatalf("WithAttrs() returned %T, want *Handler", wrapped)
	}

	logger := slog.New(wrapped)
	ctx := trace.ContextWithTraceID(t.Context(), "trace-xyz")
	logger.InfoContext(ctx, "test message")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("failed to unmarshal log record: %v", err)
	}

	if record["service"] != "core" {
		t.Errorf("service = %v, want %q", record["service"], "core")
	}
	if record["traceId"] != "trace-xyz" {
		t.Errorf("traceId = %v, want %q", record["traceId"], "trace-xyz")
	}
}

func TestHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	handler := NewHandler(base)

	wrapped := handler.WithGroup("request")

	if _, ok := wrapped.(*Handler); !ok {
		t.Fatalf("WithGroup() returned %T, want *Handler", wrapped)
	}

	logger := slog.New(wrapped)
	ctx := trace.ContextWithTraceID(t.Context(), "trace-grouped")
	logger.InfoContext(ctx, "test message", slog.String("key", "value"))

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("failed to unmarshal log record: %v", err)
	}

	// Handle adds traceId via record.AddAttrs before delegating to the base
	// handler, so the base handler's own WithGroup applies to it just like
	// any other attribute — it ends up nested under "request", not top-level.
	group, ok := record["request"].(map[string]any)
	if !ok {
		t.Fatalf("expected %q group in record, got %v", "request", record)
	}
	if group["key"] != "value" {
		t.Errorf("request.key = %v, want %q", group["key"], "value")
	}
	if group["traceId"] != "trace-grouped" {
		t.Errorf("request.traceId = %v, want %q", group["traceId"], "trace-grouped")
	}
}

func TestNewHandler(t *testing.T) {
	base := slog.NewJSONHandler(&bytes.Buffer{}, nil)
	handler := NewHandler(base)

	if handler == nil {
		t.Fatal("NewHandler() returned nil")
	}
	if handler.Handler != base {
		t.Error("NewHandler() did not wrap the given handler")
	}
}
