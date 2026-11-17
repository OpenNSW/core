package systemlog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/OpenNSW/nsw-srilanka/internal/securitylog"
)

func TestStampCategoryDefaultsPlainLines(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(StampCategory(slog.NewJSONHandler(&buf, nil)))
	logger.Info("ops line")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if line["category"] != Category {
		t.Fatalf("category = %v, want %q", line["category"], Category)
	}
}

func TestStampCategoryPreservesSecurity(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(StampCategory(slog.NewJSONHandler(&buf, nil)))
	securitylog.Event(context.Background(), logger, "http denied", "reason", "unauthenticated")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if line["category"] != securitylog.Category {
		t.Fatalf("category = %v, want %q", line["category"], securitylog.Category)
	}
}
