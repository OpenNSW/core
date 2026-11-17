package audit

import (
	"context"
	"log/slog"

	sharedaudit "github.com/OpenNSW/core/shared/audit"
)

// Core event types emitted by core/payment and core/storage. They are not the
// NSW EventType constants, so the adapter maps them into this package's
// vocabulary before they reach Client.
const (
	coreEventPayment       = "PAYMENT"
	coreEventStorage       = "STORAGE"
	coreEventPresignUpload = "PRESIGN_UPLOAD"
)

// CoreAdapter bridges core/shared/audit.Auditor (used by core/payment and
// core/storage via WithAuditor) to the application Auditor.
type CoreAdapter struct {
	auditor Auditor
}

// NewCoreAdapter wraps an application Auditor as a shared/audit.Auditor.
func NewCoreAdapter(a Auditor) *CoreAdapter {
	return &CoreAdapter{auditor: a}
}

// Audit implements sharedaudit.Auditor. Actor, trace ID, and timestamp are left
// for Client.Audit to derive from context; core events do not populate those
// fields.
func (a *CoreAdapter) Audit(ctx context.Context, e sharedaudit.Event) {
	if a == nil || a.auditor == nil {
		return
	}

	eventType, action, targetType, ok := mapCoreEvent(e)
	if !ok {
		slog.WarnContext(ctx, "audit: dropping unmapped core event",
			"eventType", e.EventType, "action", e.Action)
		return
	}

	var metadata map[string]any
	if e.Details != nil {
		metadata = e.Details.Metadata()
	}

	a.auditor.Audit(ctx, Event{
		EventType:  eventType,
		Action:     action,
		TargetType: targetType,
		TargetID:   e.TargetID,
		Failure:    e.Status == sharedaudit.StatusFailure,
		Metadata:   metadata,
	})
}

func mapCoreEvent(e sharedaudit.Event) (EventType, Action, TargetType, bool) {
	action := Action(e.Action)
	switch e.EventType {
	case coreEventPayment, string(EventPayment):
		return EventPayment, action, TargetPayment, true
	case coreEventStorage, string(EventStorage):
		return EventStorage, action, TargetStorage, true
	case coreEventPresignUpload:
		return EventStorage, ActionPresignUpload, TargetStorage, true
	default:
		return "", "", "", false
	}
}
