package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// HTTPDispatcher defines the function signature for executing external system integrations.
type HTTPDispatcher func(ctx context.Context, url string, taskID string, payload map[string]any) error

// DefaultHTTPDispatcher makes a real HTTP POST request containing the task ID and namespaced task payload.
func DefaultHTTPDispatcher(ctx context.Context, url string, taskID string, payload map[string]any) error {
	bodyMap := map[string]any{
		"task_id": taskID,
		"data":    payload,
	}

	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return fmt.Errorf("failed to marshal dispatch request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute external http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("external review system returned non-success code: %d", resp.StatusCode)
	}

	return nil
}

// ExternalReviewPlugin manages asynchronous delegation of task steps to third-party government agencies.
type ExternalReviewPlugin struct {
	dispatcher HTTPDispatcher
}

// NewExternalReviewPlugin returns a plugin with a custom or default HTTP dispatcher.
func NewExternalReviewPlugin(dispatcher HTTPDispatcher) *ExternalReviewPlugin {
	if dispatcher == nil {
		dispatcher = DefaultHTTPDispatcher
	}
	return &ExternalReviewPlugin{
		dispatcher: dispatcher,
	}
}

func (p *ExternalReviewPlugin) Name() string {
	return "generic_external_review"
}

// ExternalReviewConfig holds properties decoded from the TaskTemplate's JSON configuration.
type ExternalReviewConfig struct {
	ExternalURL string `json:"external_url"`
}

func (p *ExternalReviewPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	var cfg ExternalReviewConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return fmt.Errorf("failed to parse external review plugin config: %w", err)
	}

	if cfg.ExternalURL == "" {
		return fmt.Errorf("missing 'external_url' in external review plugin config")
	}

	ctx.Record.Status = "QUEUED_EXTERNALLY"
	log.Printf("[Plugin: generic_external_review] Dispatching task %s to external URL: %s", ctx.Record.TaskID, cfg.ExternalURL)

	err := p.dispatcher(ctx.Context, cfg.ExternalURL, ctx.Record.TaskID, ctx.Record.Data)
	if err != nil {
		return fmt.Errorf("external dispatch failed: %w", err)
	}

	log.Printf("[Plugin: generic_external_review] Successfully dispatched task %s (active step: %s)", ctx.Record.TaskID, ctx.Record.ActiveActivityID)
	return nil
}
