// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

const (
	traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	spanID  = "00f067aa0ba902b7"
)

func spanContext(t *testing.T) trace.SpanContext {
	t.Helper()
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex(spanID)
	if err != nil {
		t.Fatal(err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid})
}

// logRecord logs one record through h, with a span in the context when
// withSpan is set.
func logRecord(t *testing.T, h slog.Handler, withSpan bool) {
	t.Helper()
	ctx := t.Context()
	if withSpan {
		ctx = trace.ContextWithSpanContext(ctx, spanContext(t))
	}
	slog.New(h).InfoContext(ctx, "test message")
}

func TestHandler_Handle(t *testing.T) {
	tests := []struct {
		name     string
		withSpan bool
	}{
		{name: "attaches trace_id and span_id when context carries a span", withSpan: true},
		{name: "omits them when context has no span", withSpan: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logRecord(t, NewHandler(slog.NewJSONHandler(&buf, nil)), tt.withSpan)

			var record map[string]any
			if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
				t.Fatalf("failed to unmarshal log record: %v", err)
			}

			gotTrace, hasTrace := record["trace_id"]
			gotSpan, hasSpan := record["span_id"]
			if !tt.withSpan {
				if hasTrace || hasSpan {
					t.Fatalf("expected no trace_id/span_id, got %v", record)
				}
				return
			}
			if gotTrace != traceID {
				t.Errorf("trace_id = %v, want %q", gotTrace, traceID)
			}
			if gotSpan != spanID {
				t.Errorf("span_id = %v, want %q", gotSpan, spanID)
			}
		})
	}
}

func TestHandler_WithAttrsAndGroupKeepDecorator(t *testing.T) {
	var buf bytes.Buffer
	handler := NewHandler(slog.NewJSONHandler(&buf, nil))

	withAttrs := handler.WithAttrs([]slog.Attr{slog.String("service", "core")})
	if _, ok := withAttrs.(*Handler); !ok {
		t.Fatalf("WithAttrs() returned %T, want *Handler", withAttrs)
	}
	withGroup := withAttrs.WithGroup("req")
	if _, ok := withGroup.(*Handler); !ok {
		t.Fatalf("WithGroup() returned %T, want *Handler", withGroup)
	}

	logRecord(t, withAttrs, true)
	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("failed to unmarshal log record: %v", err)
	}
	if record["service"] != "core" {
		t.Errorf("service = %v, want core", record["service"])
	}
	if record["trace_id"] != traceID {
		t.Errorf("trace_id = %v, want %q", record["trace_id"], traceID)
	}
}
