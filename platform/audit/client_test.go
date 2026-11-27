// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/OpenNSW/core/trace"

	"github.com/OpenNSW/core/platform/authn"
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

// An event's case ID reaches its record; an event about no case leaves it empty.
func TestClient_Audit_CaseID(t *testing.T) {
	client, sink := NewWithCapture()
	client.Audit(context.Background(), Event{EventType: EventTask, Action: ActionRead, TargetType: TargetTask, TargetID: "task-1", CaseID: "case-1"})
	client.Audit(context.Background(), Event{EventType: EventStorage, Action: ActionRead, TargetType: TargetStorage})

	recs := sink.Records()
	if recs[0].CaseID != "case-1" {
		t.Errorf("CaseID = %q, want case-1", recs[0].CaseID)
	}
	if recs[1].CaseID != "" {
		t.Errorf("CaseID = %q, want empty", recs[1].CaseID)
	}
}

// A user's record names the OU they act for; a machine client's does not.
func TestClient_Audit_ActorOU(t *testing.T) {
	for name, tc := range map[string]struct {
		principal *authn.Principal
		want      string
	}{
		"user":            {&authn.Principal{Kind: authn.KindUser, UserID: "user-1", OUHandle: "acme-exports"}, "acme-exports"},
		"user with no OU": {&authn.Principal{Kind: authn.KindUser, UserID: "user-1"}, ""},
		"machine client":  {&authn.Principal{Kind: authn.KindClient, ClientID: "service-1", OUHandle: "customs"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			client, sink := NewWithCapture()
			client.Audit(authn.ContextWithPrincipal(context.Background(), tc.principal), Event{EventType: EventTask})
			if got := sink.Records()[0].ActorOU; got != tc.want {
				t.Errorf("ActorOU = %q, want %q", got, tc.want)
			}
		})
	}
}

// An event can name its actor's type, ID or both in place of the ones derived
// from the principal. A named actor ID drops the principal's OU.
func TestClient_Audit_NamedActor(t *testing.T) {
	user := &authn.Principal{Kind: authn.KindUser, UserID: "user-1", OUHandle: "acme-exports"}
	for name, tc := range map[string]struct {
		principal      *authn.Principal
		actorType      ActorType
		actorID        string
		wantType       ActorType
		wantID, wantOU string
	}{
		"admin user":            {principal: user, actorType: ActorAdmin, wantType: ActorAdmin, wantID: "user-1", wantOU: "acme-exports"},
		"partner, no principal": {actorType: ActorService, actorID: "slpa", wantType: ActorService, wantID: "slpa"},
		"ID only":               {principal: user, actorID: "on-behalf", wantType: ActorMember, wantID: "on-behalf"},
		"nothing named":         {principal: user, wantType: ActorMember, wantID: "user-1", wantOU: "acme-exports"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if tc.principal != nil {
				ctx = authn.ContextWithPrincipal(ctx, tc.principal)
			}
			client, sink := NewWithCapture()
			client.Audit(ctx, Event{EventType: EventConsignment, ActorType: tc.actorType, ActorID: tc.actorID})

			r := sink.Records()[0]
			if r.ActorType != tc.wantType || r.ActorID != tc.wantID || r.ActorOU != tc.wantOU {
				t.Errorf("actor = %s/%s/%q, want %s/%s/%q", r.ActorType, r.ActorID, r.ActorOU, tc.wantType, tc.wantID, tc.wantOU)
			}
		})
	}
}

// LogSink writes the case and the actor's OU as their own attributes, and
// leaves each out when it is empty.
func TestLogSink_WritesCaseAndActorOU(t *testing.T) {
	for name, tc := range map[string]struct {
		caseID, actorOU string
	}{
		"set":   {caseID: "case-1", actorOU: "acme-exports"},
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			NewLogSink(slog.New(slog.NewJSONHandler(&buf, nil))).Write(context.Background(), Record{
				EventType: EventTask, Action: ActionRead, Status: StatusSuccess,
				ActorType: ActorMember, ActorID: "u1", ActorOU: tc.actorOU,
				TargetType: TargetTask, TargetID: "task-1", CaseID: tc.caseID,
			})

			var line map[string]any
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("unmarshal log: %v", err)
			}
			for attr, want := range map[string]string{"caseID": tc.caseID, "actorOU": tc.actorOU} {
				got, present := line[attr]
				if want == "" && present {
					t.Errorf("%s = %v, want it left out", attr, got)
				}
				if want != "" && got != want {
					t.Errorf("%s = %v, want %q", attr, got, want)
				}
			}
		})
	}
}
