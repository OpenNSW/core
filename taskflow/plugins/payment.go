package plugins

import (
	"encoding/json"
	"fmt"
	"log"
)

// PaymentPlugin implements the generic_payment plugin.
// It initiates a payment step externally and transitions the task record to "PENDING_PAYMENT".
type PaymentPlugin struct {
	dispatcher Dispatcher
}

// NewPaymentPlugin creates a new PaymentPlugin.
func NewPaymentPlugin(dispatcher Dispatcher) *PaymentPlugin {
	if dispatcher == nil {
		dispatcher = DefaultHTTPDispatcher
	}
	return &PaymentPlugin{
		dispatcher: dispatcher,
	}
}

func (p *PaymentPlugin) Name() string {
	return "generic_payment"
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

	ctx.Record.Status = "PENDING_PAYMENT"

	log.Printf("[Plugin: generic_payment] Dispatching payment request for task %s to URL: %s", ctx.Record.TaskID, cfg.PaymentServiceURL)

	err := p.dispatcher(ctx.Context, cfg.PaymentServiceURL, ctx.Record.TaskID, ctx.Record.Data)
	if err != nil {
		return fmt.Errorf("payment dispatch failed: %w", err)
	}

	log.Printf("[Plugin: generic_payment] Successfully dispatched payment step for task %s (active step: %s)", ctx.Record.TaskID, ctx.Record.SubTaskNodeID)
	return ErrSuspended
}
