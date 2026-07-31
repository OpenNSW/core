// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package logging provides slog.Handler decorators shared across the server.
package logging

import (
	"context"
	"log/slog"

	"github.com/OpenNSW/core/trace"
)

// Handler wraps an slog.Handler and attaches the request's trace ID.
type Handler struct {
	slog.Handler
}

// NewHandler wraps next so every log record made via a *Context slog
// call automatically gets "traceId" attached.
func NewHandler(next slog.Handler) *Handler {
	return &Handler{Handler: next}
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	if traceID := trace.GetTraceID(ctx); traceID != "" {
		record.AddAttrs(slog.String("traceId", traceID))
	}
	return h.Handler.Handle(ctx, record)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{Handler: h.Handler.WithGroup(name)}
}
