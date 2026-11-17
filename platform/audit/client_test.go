package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/OpenNSW/core/trace"

	"github.com/OpenNSW/nsw-srilanka/internal/authn"
)

func TestClient_Audit_Member(t *testing.T) {
	client, sink := NewWithCapture()

	ctx := authn.ContextWithPrincipal(context.Background(), &authn.Principal{
		Kind:   authn.KindUser,
		UserID: "user-1",
		Roles:  []string{"trader"},
	})
	ctx = trace.ContextWithTraceID(ctx, "trace-1")

	client.Audit(ctx, Event{
		EventType:  EventConsignment,
		Action:     ActionCreate,
		TargetType: TargetConsignment,
		TargetID:   "con-123",
		Failure:    false,
		Message:    map[string]string{"foo": "bar"},
	})

	recs := sink.Records()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r.ActorType != ActorMember {
		t.Errorf("ActorType = %s, want %s", r.ActorType, ActorMember)
	}
	if r.ActorID != "user-1" {
		t.Errorf("ActorID = %s, want user-1", r.ActorID)
	}
	if r.TraceID != "trace-1" {
		t.Errorf("TraceID = %s, want trace-1", r.TraceID)
	}
	if r.Status != StatusSuccess {
		t.Errorf("Status = %s, want %s", r.Status, StatusSuccess)
	}
}

func TestClient_Audit_FailureAndService(t *testing.T) {
	client, sink := NewWithCapture()

	ctx := authn.ContextWithPrincipal(context.Background(), &authn.Principal{
		Kind:     authn.KindClient,
		ClientID: "service-1",
	})
	client.Audit(ctx, Event{
		EventType:  EventTask,
		Action:     ActionUpdate,
		TargetType: TargetTask,
		Failure:    true,
	})

	r := sink.Records()[0]
	if r.ActorType != ActorService || r.ActorID != "service-1" {
		t.Fatalf("actor = %s/%s", r.ActorType, r.ActorID)
	}
	if r.Status != StatusFailure {
		t.Fatalf("Status = %s, want %s", r.Status, StatusFailure)
	}
}

func TestClient_Audit_SystemAnonymous(t *testing.T) {
	client, sink := NewWithCapture()
	client.Audit(context.Background(), Event{
		EventType:  EventStorage,
		Action:     ActionDelete,
		TargetType: TargetStorage,
	})
	r := sink.Records()[0]
	if r.ActorType != ActorSystem || r.ActorID != "anonymous" {
		t.Fatalf("actor = %s/%s", r.ActorType, r.ActorID)
	}
}

func TestClient_NilAndNoSinks(t *testing.T) {
	var c *Client
	c.Audit(context.Background(), Event{EventType: EventConsignment})
	New().Audit(context.Background(), Event{EventType: EventConsignment})
}

func TestLogSink_WritesCategoryAudit(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	sink := NewLogSink(logger)
	sink.Write(context.Background(), Record{
		EventType:  EventConsignment,
		Action:     ActionCreate,
		Status:     StatusSuccess,
		ActorType:  ActorMember,
		ActorID:    "u1",
		TargetType: TargetConsignment,
		TargetID:   "c1",
	})

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if line["category"] != Category {
		t.Fatalf("category = %v, want %q", line["category"], Category)
	}
	if line["eventType"] != string(EventConsignment) {
		t.Fatalf("eventType = %v", line["eventType"])
	}
}

func TestLogSink_NilLoggerDiscards(t *testing.T) {
	sink := NewLogSink(nil)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	sink.Write(context.Background(), Record{
		EventType:  EventTask,
		Action:     ActionUpdate,
		Status:     StatusFailure,
		ActorType:  ActorMember,
		ActorID:    "u1",
		TargetType: TargetTask,
	})
	if buf.Len() != 0 {
		t.Fatalf("nil LogSink wrote to slog.Default: %s", buf.String())
	}
}

func TestClient_FansOutToMultipleSinks(t *testing.T) {
	a, b := &CaptureSink{}, &CaptureSink{}
	client := New()
	client.RegisterSink(a)
	client.RegisterSink(b)
	client.Audit(context.Background(), Event{
		EventType:  EventConsignment,
		Action:     ActionCreate,
		TargetType: TargetConsignment,
	})
	if len(a.Records()) != 1 || len(b.Records()) != 1 {
		t.Fatalf("want 1 record each, got %d and %d", len(a.Records()), len(b.Records()))
	}
}
