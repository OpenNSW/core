package securitylog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
)

func TestEventWritesCategorySecurity(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	Event(context.Background(), logger, "http denied",
		"method", "GET",
		"path", "/api/v1/tasks/x",
		"callerKind", "anonymous",
		"callerID", "",
		"status", 401,
		"reason", "unauthenticated",
	)

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if line["category"] != Category {
		t.Fatalf("category = %v, want %q", line["category"], Category)
	}
	if line["msg"] != "http denied" {
		t.Fatalf("msg = %v", line["msg"])
	}
	if line["reason"] != "unauthenticated" {
		t.Fatalf("reason = %v", line["reason"])
	}
	if line["level"] != "WARN" {
		t.Fatalf("level = %v, want WARN", line["level"])
	}
}

func TestAnnotateFillsSlot(t *testing.T) {
	ann := &Annotation{}
	ctx := WithAnnotation(context.Background(), ann)
	wantErr := errors.New("sig mismatch")

	Annotate(ctx, "webhook auth failed",
		"reason", "webhook_auth_failed",
		"partner", "slpa",
		"error", wantErr,
		"ignored", "x",
		"status", 401, // non-error / non-string value ignored
	)

	if !ann.Annotated() {
		t.Fatal("expected Annotated")
	}
	if ann.Msg != "webhook auth failed" {
		t.Fatalf("Msg = %q", ann.Msg)
	}
	if ann.Reason != "webhook_auth_failed" {
		t.Fatalf("Reason = %q", ann.Reason)
	}
	if ann.Partner != "slpa" {
		t.Fatalf("Partner = %q", ann.Partner)
	}
	if !errors.Is(ann.Err, wantErr) {
		t.Fatalf("Err = %v", ann.Err)
	}
}

func TestAnnotateNoopWithoutSlot(t *testing.T) {
	Annotate(context.Background(), "webhook auth failed", "reason", "webhook_auth_failed")
}
