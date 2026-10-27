// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/OpenNSW/core/payment"
)

// StatePendingPayment is the record state of a step waiting for its payment to settle.
const StatePendingPayment = "PENDING_PAYMENT"

// defaultPaymentExpiry is how long a checkout stays payable when the step sets no expires_in.
const defaultPaymentExpiry = 24 * time.Hour

// CheckoutService is the slice of payment.PaymentService the payment plugin uses.
type CheckoutService interface {
	ListAvailableMethods(ctx context.Context, payee payment.Payee) ([]payment.GatewayInfo, error)
	CreateCheckoutSession(ctx context.Context, req payment.CreateCheckoutRequest) (*payment.CreateCheckoutResponse, error)
}

// MetadataFunc derives checkout metadata for a step from its context and its raw plugin config,
// for values the host reads back later, such as the format of its payment reference. Its keys
// must not repeat ones the step's own "metadata" sets.
type MetadataFunc func(ctx PluginContext, config json.RawMessage) (map[string]string, error)

// PaymentOption configures a PaymentPlugin.
type PaymentOption func(*PaymentPlugin)

// WithMetadata adds the metadata fn derives to every checkout the plugin creates.
func WithMetadata(fn MetadataFunc) PaymentOption {
	return func(p *PaymentPlugin) { p.metadata = fn }
}

// PaymentPlugin implements the PAYMENT task type. It opens a checkout for the step's payee
// through core/payment, records the payment facts in the step's output namespace, moves the
// record to PENDING_PAYMENT and suspends until the payment service completes the step on
// settlement.
type PaymentPlugin struct {
	service  CheckoutService
	metadata MetadataFunc
}

// NewPaymentPlugin creates a PaymentPlugin that opens checkouts through service.
func NewPaymentPlugin(service CheckoutService, opts ...PaymentOption) TaskPlugin {
	p := &PaymentPlugin{service: service}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// PaymentConfig holds properties decoded from the TaskTemplate's JSON configuration.
type PaymentConfig struct {
	// OrgID and ServiceID name the payee. Each payment method maps them to its own account ids
	// in its "payees" config; the step never carries a gateway's ids.
	OrgID     string `json:"org_id"`
	ServiceID string `json:"service_id"`

	// ServiceName is shown to the payer; it defaults to "Payment".
	ServiceName string `json:"service_name,omitempty"`

	// Amount is charged when the step has no "amount" input.
	Amount   decimal.Decimal `json:"amount"`
	Currency string          `json:"currency"`

	// ExpiresIn is how long the checkout stays payable, as a Go duration ("48h"). Defaults to 24h.
	ExpiresIn string `json:"expires_in,omitempty"`

	// Metadata is optional pass-through data stored with the transaction.
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (p *PaymentPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	if ctx.Record == nil {
		return errors.New("payment: task record is required")
	}
	if p.service == nil {
		return errors.New("payment: checkout service is not initialized")
	}

	var cfg PaymentConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return fmt.Errorf("payment: invalid config: %w", err)
	}
	payee := payment.Payee{OrgID: strings.TrimSpace(cfg.OrgID), ServiceID: strings.TrimSpace(cfg.ServiceID)}
	if payee.OrgID == "" || payee.ServiceID == "" {
		return errors.New("payment: org_id and service_id are required")
	}
	if cfg.Currency == "" {
		return errors.New("payment: currency is required")
	}

	amount, err := resolvePaymentAmount(ctx.Inputs["amount"], cfg.Amount)
	if err != nil {
		return fmt.Errorf("payment (%s): %w", payee, err)
	}
	expiresIn, err := paymentExpiry(cfg.ExpiresIn)
	if err != nil {
		return fmt.Errorf("payment (%s): %w", payee, err)
	}
	metadata, err := p.checkoutMetadata(ctx, configRaw, cfg.Metadata)
	if err != nil {
		return fmt.Errorf("payment (%s): %w", payee, err)
	}

	selected, _ := ctx.Inputs["selected_method"].(string)
	methods, err := p.service.ListAvailableMethods(ctx.Context, payee)
	if err != nil {
		return fmt.Errorf("payment (%s): list payment methods: %w", payee, err)
	}
	method, err := selectPaymentMethod(strings.TrimSpace(selected), methods)
	if err != nil {
		return fmt.Errorf("payment (%s): %w", payee, err)
	}

	token, err := CallbackToken(ctx.Record)
	if err != nil {
		return err
	}
	resp, err := p.service.CreateCheckoutSession(ctx.Context, payment.CreateCheckoutRequest{
		GatewayID:     method,
		Payee:         payee,
		Amount:        amount,
		Currency:      cfg.Currency,
		ExpiresAt:     time.Now().Add(expiresIn),
		Metadata:      metadata,
		CallbackToken: token,
	})
	if err != nil {
		return fmt.Errorf("payment (%s): create checkout with %s: %w", payee, method, err)
	}

	ctx.Record.State = StatePendingPayment
	slog.InfoContext(ctx.Context, "payment: checkout created", "task_id", ctx.Record.TaskID,
		"payee", payee.String(), "method", method, "reference", resp.ReferenceNumber)

	if ns := ctx.OutputNamespace; ns != "" {
		if ctx.Record.Data == nil {
			ctx.Record.Data = map[string]any{}
		}
		serviceName := cfg.ServiceName
		if serviceName == "" {
			serviceName = "Payment"
		}
		ctx.Record.Data[ns] = map[string]any{
			"session_id":       resp.SessionID,
			"reference_number": resp.ReferenceNumber,
			"amount":           amount.String(),
			"currency":         cfg.Currency,
			"selected_method":  method,
			"checkout_url":     resp.CheckoutURL,
			"instructions":     resp.Instructions,
			"flow_type":        string(resp.Type),
			"service_name":     serviceName,
			"org_id":           payee.OrgID,
			"service_id":       payee.ServiceID,
		}
	}

	return ErrSuspended
}

// checkoutMetadata merges the step's static metadata with what the host's MetadataFunc derives.
// A key set by both is an error rather than a silent override.
func (p *PaymentPlugin) checkoutMetadata(ctx PluginContext, configRaw json.RawMessage, static map[string]string) (map[string]string, error) {
	metadata := maps.Clone(static)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if p.metadata == nil {
		return metadata, nil
	}
	derived, err := p.metadata(ctx, configRaw)
	if err != nil {
		return nil, err
	}
	for key, value := range derived {
		if _, clash := metadata[key]; clash {
			return nil, fmt.Errorf("metadata key %q is set by both the step and the host", key)
		}
		metadata[key] = value
	}
	return metadata, nil
}

// selectPaymentMethod picks the method to pay with from those that can pay the payee: the
// selected one, which must be among them, or, when none is selected, the only one there is.
func selectPaymentMethod(selected string, methods []payment.GatewayInfo) (string, error) {
	ids := make([]string, 0, len(methods))
	for _, m := range methods {
		ids = append(ids, m.ID)
	}
	if len(ids) == 0 {
		return "", errors.New("no payment method can pay this payee")
	}
	if selected != "" {
		if !slices.Contains(ids, selected) {
			return "", fmt.Errorf("payment method %q cannot pay this payee (available: %s)", selected, strings.Join(ids, ", "))
		}
		return selected, nil
	}
	if len(ids) > 1 {
		return "", fmt.Errorf(`input "selected_method" is required: available methods are %s`, strings.Join(ids, ", "))
	}
	return ids[0], nil
}

// resolvePaymentAmount decides the amount to charge: the step's "amount" input when supplied,
// otherwise the configured amount. Either must be positive.
func resolvePaymentAmount(input any, configured decimal.Decimal) (decimal.Decimal, error) {
	if input == nil {
		if !configured.IsPositive() {
			return decimal.Decimal{}, errors.New(`amount is required and must be positive when no "amount" input is supplied`)
		}
		return configured, nil
	}
	amount, err := decimalFromAny(input)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf(`input "amount" is invalid: %w`, err)
	}
	if !amount.IsPositive() {
		return decimal.Decimal{}, fmt.Errorf(`input "amount" must be positive, got %s`, amount)
	}
	return amount, nil
}

// decimalFromAny converts a workflow input into a decimal. Inputs cross a JSON boundary, so a
// number arrives as a float64 or json.Number, or as a string from a form.
func decimalFromAny(v any) (decimal.Decimal, error) {
	switch x := v.(type) {
	case float64:
		return decimal.NewFromFloat(x), nil
	case int:
		return decimal.NewFromInt(int64(x)), nil
	case int64:
		return decimal.NewFromInt(x), nil
	case json.Number:
		return decimal.NewFromString(x.String())
	case string:
		return decimal.NewFromString(strings.TrimSpace(x))
	default:
		return decimal.Decimal{}, fmt.Errorf("unsupported type %T", v)
	}
}

func paymentExpiry(raw string) (time.Duration, error) {
	if raw == "" {
		return defaultPaymentExpiry, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("expires_in: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("expires_in must be positive, got %s", raw)
	}
	return d, nil
}
