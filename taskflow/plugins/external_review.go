// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// ExternalReviewPlugin manages asynchronous delegation of task steps to third-party government agencies.
type ExternalReviewPlugin struct {
	dispatcher Dispatcher
}

// NewExternalReviewPlugin returns a plugin with a custom or default HTTP dispatcher.
func NewExternalReviewPlugin(dispatcher Dispatcher) TaskPlugin {
	if dispatcher == nil {
		dispatcher = DefaultHTTPDispatcher
	}
	return &ExternalReviewPlugin{
		dispatcher: dispatcher,
	}
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

	ctx.Record.State = "QUEUED_EXTERNALLY"
	slog.InfoContext(ctx.Context, "external_review: dispatching", "task_id", ctx.Record.TaskID, "url", cfg.ExternalURL)

	if err := p.dispatcher(ctx.Context, cfg.ExternalURL, ctx.Record.TaskID, ctx.Record.Data); err != nil {
		return fmt.Errorf("external dispatch failed: %w", err)
	}

	return ErrSuspended
}
