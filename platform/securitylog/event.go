// Package securitylog records sparse category=security events worth alerting
// on (authn failures, authz denials, bad callback tokens). Lines name who
// called, which route, the status, and a short reason. Request and response
// payloads are never logged: forms are JSON Schema–driven (jsonforms), so
// field-level redaction cannot list every PII key.
//
// Handlers do not write security lines themselves. systemlog.Middleware puts
// an Annotation slot on the request context; handlers call Annotate to add
// reason, partner, and an optional error. Middleware then writes the only
// category=security line when the status is 401/403 or the slot was annotated,
// so each denial is logged exactly once (no parallel slog.Warn from the
// handler).
package securitylog

import (
	"context"
	"log/slog"
)

// Category is the slog attribute value on every security event. Collectors
// filter on this string; keep a single definition here.
const Category = "security"

// Annotation is a request-scoped slot systemlog.Middleware allocates before
// ServeHTTP. Handlers call Annotate to fill Msg, Reason, Partner, and Err;
// the middleware reads them when writing the security line.
type Annotation struct {
	Msg     string
	Reason  string
	Partner string
	Err     error
	set     bool
}

type annotationKey struct{}

// WithAnnotation returns ctx carrying slot.
func WithAnnotation(ctx context.Context, slot *Annotation) context.Context {
	return context.WithValue(ctx, annotationKey{}, slot)
}

// FromContext returns the slot on ctx, or nil when none was allocated.
func FromContext(ctx context.Context) *Annotation {
	slot, _ := ctx.Value(annotationKey{}).(*Annotation)
	return slot
}

// Annotated reports whether Annotate was called on this slot.
func (a *Annotation) Annotated() bool {
	return a != nil && a.set
}

// Annotate records msg, reason, partner, and optional error on the request's
// Annotation slot for Middleware to include on the security line. attrs may
// include "reason" and "partner" (string) and "error" (error). Other keys are
// ignored. No-op when no slot is on ctx (handler ran outside Middleware).
func Annotate(ctx context.Context, msg string, attrs ...any) {
	slot := FromContext(ctx)
	if slot == nil {
		return
	}
	slot.set = true
	if msg != "" {
		slot.Msg = msg
	}
	for i := 0; i+1 < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if !ok {
			continue
		}
		switch key {
		case "reason":
			if s, ok := attrs[i+1].(string); ok {
				slot.Reason = s
			}
		case "partner":
			if s, ok := attrs[i+1].(string); ok {
				slot.Partner = s
			}
		case "error":
			if err, ok := attrs[i+1].(error); ok {
				slot.Err = err
			}
		}
	}
}

// Event writes one security line at warn. logger nil uses slog.Default.
// Callers must not pass request or response bodies. Prefer Annotate from
// handlers; Middleware is the intended caller of Event.
func Event(ctx context.Context, logger *slog.Logger, msg string, attrs ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.With(slog.String("category", Category)).WarnContext(ctx, msg, attrs...)
}
