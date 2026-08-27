// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package audit

import (
	"context"
	"testing"
	"time"
)

type sampleDetails struct {
	Key string
}

func (d sampleDetails) Metadata() map[string]any {
	return map[string]any{"key": d.Key}
}

func TestEvent_DetailsOwnedByEmitter(t *testing.T) {
	var recorded Event
	auditor := AuditorFunc(func(_ context.Context, e Event) { recorded = e })

	auditor.Audit(context.Background(), Event{
		EventType:  "STORAGE",
		Action:     ActionDelete,
		Status:     StatusSuccess,
		TargetType: "RESOURCE",
		TargetID:   "abc",
		Timestamp:  time.Unix(1, 0).UTC(),
		Details:    sampleDetails{Key: "abc"},
	})

	if recorded.Action != ActionDelete || recorded.Status != StatusSuccess {
		t.Fatalf("got action=%s status=%s", recorded.Action, recorded.Status)
	}
	d, ok := recorded.Details.(sampleDetails)
	if !ok || d.Key != "abc" {
		t.Fatalf("Details should round-trip as the emitter's type, got %#v", recorded.Details)
	}
	if recorded.Details.Metadata()["key"] != "abc" {
		t.Fatalf("Metadata() = %v", recorded.Details.Metadata())
	}
}

func TestEvent_NilDetails(t *testing.T) {
	e := Event{Action: ActionRead, Status: StatusFailure}
	if e.Details != nil {
		t.Fatal("zero Event must have nil Details")
	}
	if e.Action != ActionRead || e.Status != StatusFailure {
		t.Fatalf("got action=%s status=%s", e.Action, e.Status)
	}
}

// AuditorFunc adapts a function to Auditor for tests.
type AuditorFunc func(context.Context, Event)

func (f AuditorFunc) Audit(ctx context.Context, e Event) { f(ctx, e) }
