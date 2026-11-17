package audit

import (
	"context"
	"log/slog"
)

// Category is the slog attribute value on every LogSink record. Bootstrap and
// collectors filter on this string; keep a single definition here.
const Category = "audit"

// LogSink writes enriched audit records as structured slog lines with
// category=audit. A nil logger discards writes. Bootstrap passes an OTLP-only
// logger at construction so records never reach the process (pod) log and are
// not dropped while workers start.
type LogSink struct {
	logger *slog.Logger
}

// NewLogSink returns a LogSink. A nil logger discards. The category attribute
// is applied here so callers do not need to set it.
func NewLogSink(logger *slog.Logger) *LogSink {
	if logger != nil {
		logger = logger.With(slog.String("category", Category))
	}
	return &LogSink{logger: logger}
}

// Write implements Sink.
func (s *LogSink) Write(ctx context.Context, r Record) {
	if s == nil || s.logger == nil {
		return
	}
	attrs := []any{
		"eventType", string(r.EventType),
		"action", string(r.Action),
		"status", string(r.Status),
		"actorType", string(r.ActorType),
		"actorID", r.ActorID,
		"targetType", string(r.TargetType),
	}
	if r.TargetID != "" {
		attrs = append(attrs, "targetID", r.TargetID)
	}
	if r.TraceID != "" {
		attrs = append(attrs, "traceID", r.TraceID)
	}
	if len(r.Metadata) > 0 {
		attrs = append(attrs, "metadata", r.Metadata)
	}
	if len(r.Message) > 0 {
		attrs = append(attrs, "message", string(r.Message))
	}
	s.logger.InfoContext(ctx, "audit", attrs...)
}
