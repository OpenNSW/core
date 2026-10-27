// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

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

// captureHandler keeps the attributes of every record it handles.
type captureHandler struct {
	attrs [][]slog.Attr
}

func (*captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	var attrs []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	h.attrs = append(h.attrs, attrs)
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func TestHandler_DoesNotModifySharedRecord(t *testing.T) {
	ctx := trace.ContextWithSpanContext(t.Context(), spanContext(t))
	// A Record stores five attributes inline and the rest in a slice that its
	// copies share. Whether that slice has room for more depends on how it
	// grew, so try a range of sizes; several leave spare capacity.
	for n := 6; n <= 20; n++ {
		t.Run(fmt.Sprintf("%d attrs", n), func(t *testing.T) {
			record := slog.NewRecord(time.Now(), slog.LevelInfo, "test message", 0)
			for i := range n {
				record.AddAttrs(slog.Int(fmt.Sprintf("a%d", i), i))
			}

			// A fan-out that hands the same Record to each handler without
			// cloning it.
			first, second := &captureHandler{}, &captureHandler{}
			for _, h := range []slog.Handler{NewHandler(first), NewHandler(second)} {
				if err := h.Handle(ctx, record); err != nil {
					t.Fatal(err)
				}
			}

			for name, got := range map[string]*captureHandler{"first": first, "second": second} {
				attrs := got.attrs[0]
				if len(attrs) != n+2 {
					t.Errorf("%s: got %d attrs, want %d (own + trace_id + span_id)", name, len(attrs), n+2)
				}
				for _, a := range attrs {
					if a.Key == "!BUG" {
						t.Errorf("%s: record modified through a shared copy: %v", name, a.Value)
					}
				}
			}
			if got := record.NumAttrs(); got != n {
				t.Errorf("caller's record has %d attrs, want %d", got, n)
			}
		})
	}
}
