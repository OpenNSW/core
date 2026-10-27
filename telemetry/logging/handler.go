// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package logging provides an slog.Handler decorator that adds the current
// span's trace and span IDs to log records.
package logging

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// Handler wraps an slog.Handler and attaches the span context in ctx.
type Handler struct {
	slog.Handler
}

// NewHandler wraps next so every log record made via a *Context slog call
// with a span in ctx gets "trace_id" and "span_id", following OpenTelemetry's
// field names for non-OTLP logs.
//
// Wrap a handler that writes outside OTLP, such as a stdout JSON handler.
// Provider.SlogHandler already carries the span context on every record.
func NewHandler(next slog.Handler) *Handler {
	return &Handler{Handler: next}
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		// Copies of a Record share attribute storage; clone so adding ours
		// can't reach a copy another handler holds.
		record = record.Clone()
		record.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, record)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{Handler: h.Handler.WithGroup(name)}
}
