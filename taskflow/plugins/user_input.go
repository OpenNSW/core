// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"encoding/json"
	"log/slog"
)

// UserInputPlugin implements a standard human interaction / form submission step.
type UserInputPlugin struct{}

func NewUserInputPlugin() TaskPlugin {
	return &UserInputPlugin{}
}

// UserInputConfig holds properties specific to the user input step
type UserInputConfig struct {
	StatusOverride string `json:"status_override,omitempty"`
}

func (p *UserInputPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	status := "PENDING_USER"

	if len(configRaw) > 0 && string(configRaw) != "null" {
		var cfg UserInputConfig
		if err := json.Unmarshal(configRaw, &cfg); err != nil {
			slog.WarnContext(ctx.Context, "user_input: ignoring invalid config, using default status", "task_id", ctx.Record.TaskID, "error", err)
		} else if cfg.StatusOverride != "" {
			status = cfg.StatusOverride
		}
	}

	ctx.Record.State = status
	slog.DebugContext(ctx.Context, "user_input: awaiting submission", "task_id", ctx.Record.TaskID, "node_id", ctx.Record.SubTaskNodeID)
	return ErrSuspended
}
