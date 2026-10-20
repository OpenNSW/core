// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

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
//
// callbackToken identifies the step being dispatched (see CallbackToken). It is the same on every
// retry of that step's dispatch and different for every step, so the receiver can use it as an
// idempotency key, and it must be echoed on the callback.
type Dispatcher func(ctx context.Context, url string, callbackToken string, payload map[string]any) error

// DefaultHTTPDispatcher sends the payload as-is with no envelope, and the callback token in the
// X-Task-ID header.
// Callers that need a specific request shape should provide a custom dispatcher.
func DefaultHTTPDispatcher(ctx context.Context, url string, callbackToken string, payload map[string]any) error {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal dispatch payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Task-ID", callbackToken) // carry the callback token as a header, not in the body

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
