// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktypes

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/OpenNSW/core/payment"
	paymentref "github.com/OpenNSW/core/platform/payment"
	coreplugins "github.com/OpenNSW/core/taskflow/plugins"
	"github.com/shopspring/decimal"
)

// PaymentPlugin implements a custom generic_payment plugin for taskflow.
// It initiates a checkout session with the payment service and transitions
// the task record state to PENDING_PAYMENT.
type PaymentPlugin struct {
	paymentService payment.Service
}

// referenceLifetime is how long a payment reference stays payable: a year, so the
// applicant is not held to paying it within a day. A reference that never expired
// would also never pick up a change to the fee it was issued for, so this is a
// stopgap until checkouts follow such changes.
// TODO(OpenNSW/nsw-srilanka#899): follow fee changes and drop the lifetime.
const referenceLifetime = 365 * 24 * time.Hour

// NewPaymentPlugin builds the PAYMENT plugin, which opens checkouts through
// paymentService. paymentService must be non-nil.
func NewPaymentPlugin(paymentService payment.Service) *PaymentPlugin {
	if paymentService == nil {
		panic("paymentService is nil")
	}
	return &PaymentPlugin{
		paymentService: paymentService,
	}
}

type paymentConfig struct {
	// OrgID and ServiceID name the payee the fee is paid to. Each payment
	// method maps a payee to its own account ids under "payees" in the server
	// config's payment section, so a fee never carries a gateway's ids, and only
	// the methods that map the payee can collect it.
	OrgID     string          `json:"org_id"`
	ServiceID string          `json:"service_id"`
	Amount    decimal.Decimal `json:"amount"`
	Currency  string          `json:"currency"`

	// Reference names the refid format of the fee's payment reference, the
	// reference the payer pays against. A fee without one takes the default
	// format; see paymentref.RefGenerator.
	Reference *paymentReferenceConfig `json:"reference,omitempty"`
}

// paymentReferenceConfig names the refid format of a fee's payment reference
// and supplies the params it expects. Params maps a param to an absolute JSON
// Pointer into the step's inputs, as in REFID_GENERATOR, for values that vary
// per payment. Values gives a param its value as is, for codes fixed per fee.
// A param may be named in only one of the two.
type paymentReferenceConfig struct {
	Issuer string            `json:"issuer"`
	IDType string            `json:"id_type"`
	Params map[string]string `json:"params,omitempty"`
	Values map[string]string `json:"values,omitempty"`
}

func (p *PaymentPlugin) Execute(ctx pluginContext, configRaw json.RawMessage) error {
	var cfg paymentConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return configError(fmt.Errorf("payment: failed to parse generic_payment config: %w", err))
	}

	payee := payment.Payee{OrgID: strings.TrimSpace(cfg.OrgID), ServiceID: strings.TrimSpace(cfg.ServiceID)}
	if payee.OrgID == "" || payee.ServiceID == "" {
		return configError(errors.New("payment: plugin_properties.org_id and plugin_properties.service_id are required"))
	}
	if cfg.Currency == "" {
		return configError(fmt.Errorf("payment: plugin_properties.currency is required"))
	}

	// 1. Determine the payment method: the one the payer selected, which must
	// be able to pay this payee, or, when the step offers no choice, the only
	// method that can.
	methods, err := p.paymentService.ListAvailableMethods(ctx.Context, payee)
	if err != nil {
		return fmt.Errorf("payment (%s): list payment methods: %w", payee, err)
	}
	requested, _ := ctx.Inputs["selected_method"].(string)
	selectedMethod, err := selectPaymentMethod(strings.TrimSpace(requested), methods)
	if err != nil {
		return fmt.Errorf("payment (%s): %w", payee, err)
	}

	// 2. Resolve the amount to charge. If a workflow input is available, it takes
	// precedence over the artifact's static amount. This is the last
	// checkpoint before it becomes an actual charge so the logical validation is done to
	// make sure the payment amount is acceptable.
	amount, err := resolvePaymentAmount(ctx.Inputs["amount"], cfg.Amount)
	if err != nil {
		return fmt.Errorf("payment (%s): %w", payee, err)
	}
	currency := cfg.Currency

	referenceMetadata, err := paymentReferenceMetadata(ctx.Inputs, cfg.Reference)
	if err != nil {
		return configError(fmt.Errorf("payment (%s): reference: %w", payee, err))
	}

	// 3. Transition task state to PENDING_PAYMENT
	ctx.Record.State = "PENDING_PAYMENT"

	slog.Info("task payment: initiating checkout session",
		"taskId", ctx.Record.TaskID, "payee", payee.String(), "amount", amount, "method", selectedMethod)

	// 4. Create the checkout session via core/payment. The selected gateway is
	// passed as GatewayID; the service takes the reference from
	// paymentref.RefGenerator, in the fee's format, and (for instruction-flow
	// gateways) returns the instructions to display. A method that does not
	// map the payee surfaces here as payment.ErrPayeeNotMapped, before anything
	// is persisted, naming the payee to add to the server config's payment section.
	// The settlement completes exactly this step: the payment service hands the
	// token back to the task manager when the webhook arrives.
	callbackToken, err := coreplugins.CallbackToken(ctx.Record)
	if err != nil {
		return inputError(fmt.Errorf("payment: %w", err))
	}
	resp, err := p.paymentService.CreateCheckoutSession(ctx.Context, payment.CreateCheckoutRequest{
		GatewayID:     selectedMethod,
		Payee:         payee,
		Amount:        amount,
		Currency:      currency,
		ExpiresAt:     time.Now().Add(referenceLifetime),
		Metadata:      buildPaymentMetadata(ctx.Record.TaskID, selectedMethod, referenceMetadata),
		CallbackToken: callbackToken,
	})
	if err != nil {
		err = fmt.Errorf("payment (%s): failed to create checkout session: %w", payee, err)
		if errors.Is(err, payment.ErrPayeeNotMapped) {
			return configError(err)
		}
		return classifyRemote(err)
	}

	slog.Info("task payment: checkout session registered",
		"taskId", ctx.Record.TaskID, "sessionId", resp.SessionID, "referenceNumber", resp.ReferenceNumber, "method", selectedMethod)

	// 6. Populate payment info under the active output namespace
	if ctx.OutputNamespace != "" {
		if ctx.Record.Data == nil {
			ctx.Record.Data = make(map[string]any)
		}

		pData := map[string]any{
			"session_id":       resp.SessionID,
			"reference_number": resp.ReferenceNumber,
			"amount":           amount.String(),
			"currency":         currency,
			"selected_method":  selectedMethod,
			"checkout_url":     resp.CheckoutURL,
			"instructions":     resp.Instructions,
			"flow_type":        string(resp.Type),
			"org_id":           payee.OrgID,
			"service_id":       payee.ServiceID,
		}

		ctx.Record.Data[ctx.OutputNamespace] = pData
	}

	// Suspend the workflow until the payment gateway's webhook arrives
	return ErrSuspended
}

// buildPaymentMetadata assembles the metadata persisted with the transaction:
// the task it pays for and the method it was opened with, for correlation, and
// the fee's reference format (referenceMetadata, from paymentReferenceMetadata),
// which paymentref.RefGenerator reads.
func buildPaymentMetadata(taskID, selectedMethod string, referenceMetadata map[string]string) map[string]string {
	metadata := make(map[string]string, len(referenceMetadata)+2)
	maps.Copy(metadata, referenceMetadata)
	metadata["task_id"] = taskID
	metadata["method_id"] = selectedMethod
	return metadata
}

// selectPaymentMethod picks the method to pay with from those that can pay the
// payee: the selected one, which must be among them, or, when none is
// selected, the only one there is.
func selectPaymentMethod(selected string, methods []payment.GatewayInfo) (string, error) {
	ids := make([]string, 0, len(methods))
	for _, m := range methods {
		ids = append(ids, m.ID)
	}
	if len(ids) == 0 {
		return "", configError(errors.New("no payment method can pay this payee; map it under payment.methods[].payees in the server config"))
	}
	if selected != "" {
		if !slices.Contains(ids, selected) {
			return "", inputError(fmt.Errorf("payment method %q cannot pay this payee (available: %s)", selected, strings.Join(ids, ", ")))
		}
		return selected, nil
	}
	if len(ids) > 1 {
		return "", inputError(fmt.Errorf(`input "selected_method" is required: available methods are %s`, strings.Join(ids, ", ")))
	}
	return ids[0], nil
}

// paymentReferenceMetadata resolves a fee's reference format into the checkout
// metadata paymentref.RefGenerator reads: its issuer and id_type, its params read
// through their pointers into inputs, and its fixed values. A fee without a
// format adds no keys, so its checkout takes the default format.
func paymentReferenceMetadata(inputs map[string]any, cfg *paymentReferenceConfig) (map[string]string, error) {
	if cfg == nil {
		return nil, nil
	}
	if strings.TrimSpace(cfg.Issuer) == "" || strings.TrimSpace(cfg.IDType) == "" {
		return nil, errors.New("plugin_properties.reference needs an issuer and an id_type")
	}
	pointers := make(map[string]refIDPointer, len(cfg.Params))
	for name, raw := range cfg.Params {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("plugin_properties.reference.params has an empty name")
		}
		ptr, err := parseRefIDPointer(raw, false)
		if err != nil {
			return nil, fmt.Errorf("plugin_properties.reference.params.%s: %w", name, err)
		}
		pointers[name] = ptr
	}
	for name := range cfg.Values {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("plugin_properties.reference.values has an empty name")
		}
		if _, both := pointers[name]; both {
			return nil, fmt.Errorf("plugin_properties.reference names %q in both params and values", name)
		}
	}
	params, err := readRefIDParams(inputs, nil, pointers)
	if err != nil {
		return nil, err
	}
	for name, value := range cfg.Values {
		params[name] = value
	}
	return paymentref.ReferenceMetadata(cfg.Issuer, cfg.IDType, params), nil
}

// Decides the amount to charge for a task. If the workflow input "amount" is
// supplied, it takes precedence over the artifact's configured amount. If no
// input is supplied, the configured amount will be taken. Both are validated to be
// valid as a payment amount.
func resolvePaymentAmount(input any, configured decimal.Decimal) (decimal.Decimal, error) {
	if input == nil {
		if !configured.IsPositive() {
			return decimal.Decimal{}, configError(errors.New(`plugin_properties.amount is required and must be positive when no "amount" input is supplied`))
		}
		if err := storableAmount(configured); err != nil {
			return decimal.Decimal{}, configError(fmt.Errorf("plugin_properties.amount %w", err))
		}
		return configured, nil
	}

	amount, err := decimalFromAny(input)
	if err != nil {
		return decimal.Decimal{}, inputError(fmt.Errorf(`input "amount" is invalid: %w`, err))
	}
	if !amount.IsPositive() {
		return decimal.Decimal{}, inputError(fmt.Errorf(`input "amount" must be positive, got %s`, amount.String()))
	}
	if err := storableAmount(amount); err != nil {
		return decimal.Decimal{}, inputError(fmt.Errorf(`input "amount" %w`, err))
	}
	return amount, nil
}

// maxAmount is the largest amount payment_transactions.amount, a
// NUMERIC(15, 2) column, can hold.
var maxAmount = decimal.RequireFromString("9999999999999.99")

// storableAmount reports an amount the payment's transaction can't store: one
// past maxAmount, which the database refuses, or one with more than two
// decimal places, which it would round, charging a different amount than the
// step was given. Retrying can't fix either, so the step parks at once.
func storableAmount(amount decimal.Decimal) error {
	if amount.GreaterThan(maxAmount) {
		return fmt.Errorf("must be at most %s, got %s", maxAmount.StringFixed(2), amount.String())
	}
	if !amount.Equal(amount.Round(2)) {
		return fmt.Errorf("must have at most two decimal places, got %s", amount.String())
	}
	return nil
}

// decimalFromAny converts a workflow input value into a decimal. Workflow
// variables cross a JSON boundary at least once (from form submission -> stored
// task data -> input_mapping copy), so a number arrives here as float64;
// string and json.Number are accepted defensively for callers that pass
// amounts as quoted values.
func decimalFromAny(v any) (decimal.Decimal, error) {
	switch x := v.(type) {
	case float64:
		return decimal.NewFromFloat(x), nil
	case string:
		return decimal.NewFromString(strings.TrimSpace(x))
	case json.Number:
		return decimal.NewFromString(x.String())
	default:
		return decimal.Decimal{}, fmt.Errorf("unsupported type %T", v)
	}
}
