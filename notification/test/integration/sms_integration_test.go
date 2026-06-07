package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenNSW/nsw/backend/pkg/notifications"
	"github.com/OpenNSW/nsw/backend/pkg/notifications/providers"
)

func TestSMSIntegration(t *testing.T) {
	received := make(chan map[string]any, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("unmarshal body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	notificationsJSON, err := json.Marshal(map[string]any{
		"sms": map[string]any{
			"baseURL":  srv.URL,
			"sidCode":  "TEST_SID",
			"userName": "test_user",
			"password": "test_pass",
		},
	})
	if err != nil {
		t.Fatalf("marshal notifications config: %v", err)
	}
	notificationsPath := filepath.Join(t.TempDir(), "notifications.json")
	if err := os.WriteFile(notificationsPath, notificationsJSON, 0o600); err != nil {
		t.Fatalf("write notifications file: %v", err)
	}

	manager, err := notifications.NewManager(
		notifications.Config{Path: notificationsPath},
		providers.NewSMSProvider(),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	t.Run("send sms", func(t *testing.T) {
		err := manager.Send(t.Context(), notifications.Request{
			Channel: notifications.ChannelSMS,
			To:      "+1234567890",
			Body:    "NSW notifications integration test.",
		})
		if err != nil {
			t.Fatalf("Send: %v", err)
		}

		select {
		case payload := <-received:
			if got := payload["phoneNumber"]; got != "+1234567890" {
				t.Errorf("phoneNumber = %q, want %q", got, "+1234567890")
			}
			if got := payload["data"]; got != "NSW notifications integration test." {
				t.Errorf("data = %q, want %q", got, "NSW notifications integration test.")
			}
			if got := payload["userName"]; got != "test_user" {
				t.Errorf("userName = %q, want %q", got, "test_user")
			}
			if got := payload["sIDCode"]; got != "TEST_SID" {
				t.Errorf("sIDCode = %q, want %q", got, "TEST_SID")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("mock server did not receive request within 3s")
		}
	})
}
