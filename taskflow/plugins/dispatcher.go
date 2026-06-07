package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Dispatcher defines the function signature for executing external system integrations.
type Dispatcher func(ctx context.Context, url string, taskID string, payload map[string]any) error

// DefaultHTTPDispatcher sends the payload as-is with no envelope.
// Callers that need a specific request shape should provide a custom dispatcher.
func DefaultHTTPDispatcher(ctx context.Context, url string, taskID string, payload map[string]any) error {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal dispatch payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Task-ID", taskID) // carry task ID as a header, not in the body

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http dispatch failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("external system returned %d: %s", resp.StatusCode, body)
	}

	return nil
}
