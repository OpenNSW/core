package notifications

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMap(t *testing.T) {
	t.Parallel()

	t.Run("valid json", func(t *testing.T) {
		t.Parallel()
		f := writeTempJSON(t, `{"email":{"host":"localhost"}}`)
		got, err := loadConfigMap(f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := got["email"]; !ok {
			t.Error("expected key \"email\" in result")
		}
	})

	t.Run("file not found", func(t *testing.T) {
		t.Parallel()
		_, err := loadConfigMap("/nonexistent/path.json")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		f := writeTempJSON(t, `not-json`)
		_, err := loadConfigMap(f)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func writeTempJSON(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return f
}

// Ensure loadConfigMap preserves raw JSON for provider-specific unmarshaling.
func TestLoadConfigMap_RawPreserved(t *testing.T) {
	t.Parallel()
	payload := `{"sms":{"api_key":"secret","timeout":30}}`
	f := writeTempJSON(t, payload)

	got, err := loadConfigMap(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var sms struct {
		APIKey  string `json:"api_key"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(got["sms"], &sms); err != nil {
		t.Fatalf("unmarshal sms blob: %v", err)
	}
	if sms.APIKey != "secret" {
		t.Errorf("api_key = %q, want %q", sms.APIKey, "secret")
	}
	if sms.Timeout != 30 {
		t.Errorf("timeout = %d, want 30", sms.Timeout)
	}
}
