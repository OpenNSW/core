package audit_test

import (
	"context"
	"testing"

	sharedaudit "github.com/OpenNSW/core/shared/audit"
	nswaudit "github.com/OpenNSW/nsw-srilanka/internal/audit"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureAuditor struct {
	events []nswaudit.Event
}

func (c *captureAuditor) Audit(_ context.Context, e nswaudit.Event) {
	c.events = append(c.events, e)
}

type stubDetails map[string]any

func (d stubDetails) Metadata() map[string]any { return d }

func TestCoreAdapter_Payment(t *testing.T) {
	cap := &captureAuditor{}
	adapter := nswaudit.NewCoreAdapter(cap)

	adapter.Audit(context.Background(), sharedaudit.Event{
		EventType:  "PAYMENT",
		Action:     sharedaudit.ActionCreate,
		Status:     sharedaudit.StatusSuccess,
		TargetType: "RESOURCE",
		TargetID:   "REF-1",
		Details: stubDetails{
			"gateway_id": "govpay",
			"status":     "PENDING",
		},
	})

	require.Len(t, cap.events, 1)
	got := cap.events[0]
	assert.Equal(t, nswaudit.EventPayment, got.EventType)
	assert.Equal(t, nswaudit.ActionCreate, got.Action)
	assert.Equal(t, nswaudit.TargetPayment, got.TargetType)
	assert.Equal(t, "REF-1", got.TargetID)
	assert.Equal(t, "govpay", got.Metadata["gateway_id"])
	assert.Equal(t, "PENDING", got.Metadata["status"])
	assert.False(t, got.Failure)
}

func TestCoreAdapter_StoragePresignFailure(t *testing.T) {
	cap := &captureAuditor{}
	adapter := nswaudit.NewCoreAdapter(cap)

	adapter.Audit(context.Background(), sharedaudit.Event{
		EventType:  "PRESIGN_UPLOAD",
		Action:     sharedaudit.ActionCreate,
		Status:     sharedaudit.StatusFailure,
		TargetType: "RESOURCE",
		TargetID:   "obj-1",
		Details: stubDetails{
			"filename": "doc.pdf",
			"error":    "boom",
		},
	})

	require.Len(t, cap.events, 1)
	got := cap.events[0]
	assert.Equal(t, nswaudit.EventStorage, got.EventType)
	assert.Equal(t, nswaudit.ActionPresignUpload, got.Action)
	assert.Equal(t, nswaudit.TargetStorage, got.TargetType)
	assert.Equal(t, "obj-1", got.TargetID)
	assert.True(t, got.Failure)
	assert.Equal(t, "boom", got.Metadata["error"])
	assert.Equal(t, "doc.pdf", got.Metadata["filename"])
}

func TestCoreAdapter_UnknownEventTypeDropped(t *testing.T) {
	cap := &captureAuditor{}
	adapter := nswaudit.NewCoreAdapter(cap)

	adapter.Audit(context.Background(), sharedaudit.Event{
		EventType: "UNKNOWN",
		Action:    sharedaudit.ActionRead,
	})

	assert.Empty(t, cap.events)
}

func TestCoreAdapter_NilSafe(t *testing.T) {
	assert.NotPanics(t, func() {
		var adapter *nswaudit.CoreAdapter
		adapter.Audit(context.Background(), sharedaudit.Event{EventType: "PAYMENT"})
		nswaudit.NewCoreAdapter(nil).Audit(context.Background(), sharedaudit.Event{EventType: "PAYMENT"})
	})
}
