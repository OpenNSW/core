// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// PaymentPlugin implements the generic_payment plugin.
// It initiates a payment step externally and transitions the task record to "PENDING_PAYMENT".
type PaymentPlugin struct {
	dispatcher Dispatcher
}

// NewPaymentPlugin creates a new PaymentPlugin.
func NewPaymentPlugin(dispatcher Dispatcher) TaskPlugin {
	if dispatcher == nil {
		dispatcher = DefaultHTTPDispatcher
	}
	return &PaymentPlugin{
		dispatcher: dispatcher,
	}
}

// PaymentConfig holds properties decoded from the TaskTemplate's JSON configuration.
type PaymentConfig struct {
	PaymentServiceURL string `json:"payment_service_url"`
}

func (p *PaymentPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	var cfg PaymentConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return fmt.Errorf("failed to parse generic_payment config: %w", err)
	}

	if cfg.PaymentServiceURL == "" {
		return fmt.Errorf("missing 'payment_service_url' in generic_payment config")
	}

	ctx.Record.State = "PENDING_PAYMENT"

	slog.InfoContext(ctx.Context, "payment_plugin: dispatching", "task_id", ctx.Record.TaskID, "url", cfg.PaymentServiceURL)

	if err := p.dispatcher(ctx.Context, cfg.PaymentServiceURL, ctx.Record.TaskID, ctx.Record.Data); err != nil {
		return fmt.Errorf("payment dispatch failed: %w", err)
	}

	return ErrSuspended
}
