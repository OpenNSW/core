// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// APICallPlugin implements the generic_api_call plugin for FIRE_AND_FORGET tasks.
// It sends an API request to a configured URL containing the task data payload.
type APICallPlugin struct {
	dispatcher Dispatcher
}

// NewAPICallPlugin creates a new APICallPlugin.
func NewAPICallPlugin(dispatcher Dispatcher) TaskPlugin {
	if dispatcher == nil {
		dispatcher = DefaultHTTPDispatcher
	}
	return &APICallPlugin{
		dispatcher: dispatcher,
	}
}

// APICallConfig holds properties decoded from the TaskTemplate's JSON configuration.
type APICallConfig struct {
	URL string `json:"url"`
}

func (p *APICallPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	var cfg APICallConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return fmt.Errorf("failed to parse generic_api_call config: %w", err)
	}

	if cfg.URL == "" {
		return fmt.Errorf("missing 'url' in generic_api_call config")
	}

	ctx.Record.State = "DISPATCHED"

	slog.InfoContext(ctx.Context, "api_call: dispatching", "task_id", ctx.Record.TaskID, "url", cfg.URL)

	if err := p.dispatcher(ctx.Context, cfg.URL, ctx.Record.TaskID, ctx.Record.Data); err != nil {
		return fmt.Errorf("api call dispatch failed: %w", err)
	}

	return nil
}
