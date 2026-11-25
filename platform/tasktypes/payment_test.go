// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktypes

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/OpenNSW/core/payment"
	paymentref "github.com/OpenNSW/core/platform/payment"
	"github.com/OpenNSW/core/taskflow/callbacktoken"
	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/store"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePaymentAmount(t *testing.T) {
	configured := decimal.NewFromFloat(12500.00)

	tests := []struct {
		name       string
		input      any
		configured decimal.Decimal
		want       string
		wantErr    bool
	}{
		{
			name:       "workflow-supplied float64 overrides configured amount",
			input:      4500.50,
			configured: configured,
			want:       "4500.5",
		},
		{
			// float roundoff check
			name:       "workflow-supplied float64 keeps its cents exactly",
			input:      4500.57,
			configured: configured,
			want:       "4500.57",
		},
		{
			name:       "workflow-supplied string amount is accepted",
			input:      "4500.50",
			configured: configured,
			want:       "4500.5",
		},
		{
			name:       "no input falls back to configured amount",
			input:      nil,
			configured: configured,
			want:       "12500",
		},
		{
			name:       "no input and no configured amount is an error",
			input:      nil,
			configured: decimal.Decimal{},
			wantErr:    true,
		},
		{
			name:       "zero workflow-supplied amount is rejected even with a valid configured fallback",
			input:      0.0,
			configured: configured,
			wantErr:    true,
		},
		{
			name:       "negative workflow-supplied amount is rejected",
			input:      -100.0,
			configured: configured,
			wantErr:    true,
		},
		{
			name:       "unparseable string amount is rejected",
			input:      "not-a-number",
			configured: configured,
			wantErr:    true,
		},
		{
			name:       "the largest amount the transaction can store is accepted",
			input:      9999999999999.99,
			configured: configured,
			want:       "9999999999999.99",
		},
		{
			name:       "an amount past what the transaction can store is rejected",
			input:      1e36,
			configured: configured,
			wantErr:    true,
		},
		{
			name:       "an amount with more than two decimal places is rejected",
			input:      "12500.555",
			configured: configured,
			wantErr:    true,
		},
		{
			name:       "a configured amount past what the transaction can store is rejected",
			input:      nil,
			configured: decimal.RequireFromString("10000000000000"),
			wantErr:    true,
		},
		{
			name:       "decimal.Decimal is never a valid input type",
			input:      decimal.NewFromFloat(4500.50),
			configured: configured,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePaymentAmount(tt.input, tt.configured)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolvePaymentAmount(%v, %v) = %v, want error", tt.input, tt.configured, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePaymentAmount(%v, %v) unexpected error: %v", tt.input, tt.configured, err)
			}
			if got.String() != tt.want {
				t.Fatalf("resolvePaymentAmount(%v, %v) = %s, want %s", tt.input, tt.configured, got.String(), tt.want)
			}
		})
	}
}

// fakePaymentService records the request it was called with. Embedding the
// interface leaves every method but ListAvailableMethods and
// CreateCheckoutSession unimplemented, which is fine as long as Execute never
// reaches them.
type fakePaymentService struct {
	payment.Service

	// methods are the ids of the methods that can pay any payee; nil means
	// just bank_transfer.
	methods   []string
	listErr   error
	listedFor []payment.Payee

	lastReq payment.CreateCheckoutRequest
	called  bool
	resp    *payment.CreateCheckoutResponse
	err     error
}

func (f *fakePaymentService) ListAvailableMethods(_ context.Context, payee payment.Payee) ([]payment.GatewayInfo, error) {
	f.listedFor = append(f.listedFor, payee)
	ids := f.methods
	if ids == nil {
		ids = []string{"bank_transfer"}
	}
	infos := make([]payment.GatewayInfo, 0, len(ids))
	for _, id := range ids {
		infos = append(infos, payment.GatewayInfo{ID: id, IsActive: true})
	}
	return infos, f.listErr
}

func (f *fakePaymentService) CreateCheckoutSession(_ context.Context, req payment.CreateCheckoutRequest) (*payment.CreateCheckoutResponse, error) {
	f.called = true
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// The task and step IDs are UUIDs, as core mints them: the plugin builds the
// checkout's callback token from them.
const (
	paymentTestTaskID = "6aad0417-9a6d-4407-9509-2e51d8fcae99"
	paymentTestStepID = "ca7ed707-1dba-43ca-94bf-10eddf00df3c"
)

func paymentPluginCtx(inputs map[string]any) plugins.PluginContext {
	return plugins.PluginContext{
		Context: context.Background(),
		Inputs:  inputs,
		Record:  &store.TaskRecord{TaskID: paymentTestTaskID, ActiveStepID: paymentTestStepID, Data: map[string]any{}},
	}
}

const paymentConfigJSON = `{"org_id":"agency","service_id":"app_fee","currency":"USD","amount":12500}`

func TestPaymentPlugin_Execute_UsesWorkflowAmountOverConfigured(t *testing.T) {
	svc := &fakePaymentService{resp: &payment.CreateCheckoutResponse{SessionID: "session-1", ReferenceNumber: "REF-1", Type: payment.FlowTypeRedirect}}
	p := NewPaymentPlugin(svc)

	err := p.Execute(paymentPluginCtx(map[string]any{"amount": 4500.50}), []byte(paymentConfigJSON))

	require.ErrorIs(t, err, ErrSuspended)
	require.True(t, svc.called, "expected the payment service to be called")
	assert.True(t, svc.lastReq.Amount.Equal(decimal.NewFromFloat(4500.50)),
		"expected the per-case workflow amount to reach the gateway, got %s (configured amount was 12500)", svc.lastReq.Amount)
}

func TestPaymentPlugin_Execute_FallsBackToConfiguredAmountWhenNoInput(t *testing.T) {
	svc := &fakePaymentService{resp: &payment.CreateCheckoutResponse{SessionID: "session-1", ReferenceNumber: "REF-1", Type: payment.FlowTypeRedirect}}
	p := NewPaymentPlugin(svc)

	err := p.Execute(paymentPluginCtx(nil), []byte(paymentConfigJSON))

	require.ErrorIs(t, err, ErrSuspended)
	require.True(t, svc.called, "expected the payment service to be called")
	assert.True(t, svc.lastReq.Amount.Equal(decimal.NewFromInt(12500)),
		"expected the configured amount to reach the gateway, got %s", svc.lastReq.Amount)
}

// A payment reference stays payable for a year, not a day.
func TestPaymentPlugin_Execute_ReferenceStaysPayableForAYear(t *testing.T) {
	svc := &fakePaymentService{resp: &payment.CreateCheckoutResponse{SessionID: "session-1", ReferenceNumber: "REF-1", Type: payment.FlowTypeRedirect}}
	p := NewPaymentPlugin(svc)

	err := p.Execute(paymentPluginCtx(nil), []byte(paymentConfigJSON))

	require.ErrorIs(t, err, ErrSuspended)
	assert.WithinDuration(t, time.Now().Add(365*24*time.Hour), svc.lastReq.ExpiresAt, time.Minute,
		"expected the reference to stay payable for a year")
}

// The checkout carries the token for the step that dispatched it, so the
// settlement completes exactly that step.
func TestPaymentPlugin_Execute_SendsTheStepsCallbackToken(t *testing.T) {
	svc := &fakePaymentService{resp: &payment.CreateCheckoutResponse{SessionID: "session-1", ReferenceNumber: "REF-1", Type: payment.FlowTypeRedirect}}
	p := NewPaymentPlugin(svc)

	err := p.Execute(paymentPluginCtx(nil), []byte(paymentConfigJSON))

	require.ErrorIs(t, err, ErrSuspended)
	want, err := callbacktoken.Encode(paymentTestTaskID, paymentTestStepID)
	require.NoError(t, err)
	assert.Equal(t, want, svc.lastReq.CallbackToken)
}

func TestPaymentPlugin_Execute_RejectsInvalidAmountWithoutCallingGateway(t *testing.T) {
	svc := &fakePaymentService{resp: &payment.CreateCheckoutResponse{SessionID: "session-1", ReferenceNumber: "REF-1", Type: payment.FlowTypeRedirect}}
	p := NewPaymentPlugin(svc)

	ctx := paymentPluginCtx(map[string]any{"amount": -5.0})
	err := p.Execute(ctx, []byte(paymentConfigJSON))

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrSuspended)
	assert.False(t, svc.called, "the gateway must never be called with a rejected amount")
	assert.NotEqual(t, "PENDING_PAYMENT", ctx.Record.State,
		"the task must not be marked PENDING_PAYMENT when the amount is rejected")
}

const feeReferenceConfigJSON = `{"org_id":"agency","service_id":"app_fee","currency":"USD","amount":12500,
	"reference":{"issuer":"AGENCY","id_type":"fee_payment_ref","params":{"exporterId":"/exporter_id"},
		"values":{"mainCategory":"01","subCategory":"00002"}}}`

func checkoutResponse() *payment.CreateCheckoutResponse {
	return &payment.CreateCheckoutResponse{SessionID: "session-1", ReferenceNumber: "000201000020001", Type: payment.FlowTypeInstruction}
}

// defaultIssuer is the default issuer generatedFrom builds its generator with.
const defaultIssuer = "ACME"

// generatedFrom feeds a checkout's metadata to paymentref.RefGenerator, as the
// payment service does, and returns the refid call it makes.
func generatedFrom(t *testing.T, req payment.CreateCheckoutRequest) refIDCall {
	t.Helper()
	refIDs := &fakeRefIDs{}
	_, err := paymentref.NewRefGenerator(refIDs, defaultIssuer).GenerateReference(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, refIDs.calls, 1)
	return refIDs.calls[0]
}

// A fee's reference format reaches the payment service in the checkout's
// metadata: the generator draws from its issuer and id_type, with a param read
// from the inputs and the fixed values. The reference the service issues is the
// step's reference_number.
func TestPaymentPlugin_Execute_PassesFeeReferenceFormat(t *testing.T) {
	svc := &fakePaymentService{resp: checkoutResponse()}
	ctx := paymentPluginCtx(map[string]any{"exporter_id": "0002"})
	ctx.OutputNamespace = "payment"

	err := NewPaymentPlugin(svc).Execute(ctx, []byte(feeReferenceConfigJSON))

	require.ErrorIs(t, err, ErrSuspended)
	assert.Equal(t, refIDCall{issuer: "AGENCY", idType: "fee_payment_ref", params: map[string]string{
		"exporterId": "0002", "mainCategory": "01", "subCategory": "00002",
	}}, generatedFrom(t, svc.lastReq))
	assert.Equal(t, "000201000020001", ctx.Record.Data["payment"].(map[string]any)["reference_number"])
}

// A reference format that expects no params needs neither params nor values:
// the generator draws from the fee's issuer and id_type alone.
func TestPaymentPlugin_Execute_ReferenceFormatWithoutParams(t *testing.T) {
	svc := &fakePaymentService{resp: checkoutResponse()}

	err := NewPaymentPlugin(svc).Execute(paymentPluginCtx(nil),
		[]byte(`{"org_id":"agency","service_id":"app_fee","currency":"USD","amount":12500,"reference":{"issuer":"ACME","id_type":"invoice_ref"}}`))

	require.ErrorIs(t, err, ErrSuspended)
	assert.Equal(t, refIDCall{issuer: "ACME", idType: "invoice_ref", params: map[string]string{}}, generatedFrom(t, svc.lastReq))
}

// A fee without a reference format takes the default format.
func TestPaymentPlugin_Execute_DefaultReferenceFormat(t *testing.T) {
	svc := &fakePaymentService{resp: checkoutResponse()}

	err := NewPaymentPlugin(svc).Execute(paymentPluginCtx(nil), []byte(paymentConfigJSON))

	require.ErrorIs(t, err, ErrSuspended)
	call := generatedFrom(t, svc.lastReq)
	assert.Equal(t, defaultIssuer, call.issuer)
	assert.Equal(t, paymentref.DefaultReferenceIDType, call.idType)
	assert.Empty(t, call.params)
}

// A fee declaring gateway ids in its gateway_metadata has them ignored: the
// ids come from the payee's mapping in the server config, and the step's
// metadata holds only its own bookkeeping and reference format.
func TestPaymentPlugin_Execute_IgnoresLegacyGatewayMetadata(t *testing.T) {
	svc := &fakePaymentService{resp: checkoutResponse()}
	config := `{"task_code":"agency_app_fee_payment_v1","org_id":"agency","service_id":"app_fee","currency":"USD","amount":12500,
		"gateway_metadata":{"gateway_subinst_id":"004","gateway_service_id":"001","reference_issuer":"X","task_id":"other"}}`

	err := NewPaymentPlugin(svc).Execute(paymentPluginCtx(nil), []byte(config))

	require.ErrorIs(t, err, ErrSuspended)
	assert.Equal(t, map[string]string{"task_id": paymentTestTaskID, "method_id": "bank_transfer"}, svc.lastReq.Metadata)
}

// The checkout is opened for the step's payee, and the step's output names it.
func TestPaymentPlugin_Execute_OpensCheckoutForThePayee(t *testing.T) {
	svc := &fakePaymentService{resp: checkoutResponse()}
	ctx := paymentPluginCtx(nil)
	ctx.OutputNamespace = "payment"

	err := NewPaymentPlugin(svc).Execute(ctx, []byte(paymentConfigJSON))

	require.ErrorIs(t, err, ErrSuspended)
	want := payment.Payee{OrgID: "agency", ServiceID: "app_fee"}
	assert.Equal(t, []payment.Payee{want}, svc.listedFor, "methods are listed for the payee")
	assert.Equal(t, want, svc.lastReq.Payee)
	out := ctx.Record.Data["payment"].(map[string]any)
	assert.Equal(t, "agency", out["org_id"])
	assert.Equal(t, "app_fee", out["service_id"])
	assert.NotContains(t, out, "service_type")
	assert.NotContains(t, out, "service_name")
}

func TestPaymentPlugin_Execute_RequiresThePayee(t *testing.T) {
	for name, config := range map[string]string{
		"no org_id":        `{"service_id":"app_fee","currency":"USD","amount":12500}`,
		"no service_id":    `{"org_id":"agency","currency":"USD","amount":12500}`,
		"blank service_id": `{"org_id":"agency","service_id":"  ","currency":"USD","amount":12500}`,
		"task_code only":   `{"task_code":"agency_app_fee_payment_v1","currency":"USD","amount":12500}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakePaymentService{resp: checkoutResponse()}
			ctx := paymentPluginCtx(nil)

			err := NewPaymentPlugin(svc).Execute(ctx, []byte(config))

			require.ErrorContains(t, err, "org_id and plugin_properties.service_id are required")
			assert.False(t, svc.called)
			assert.NotEqual(t, "PENDING_PAYMENT", ctx.Record.State)
		})
	}
}

// The payer's choice must be a method that can pay the payee; with no choice,
// the only such method is used, and with several the step asks for one.
func TestPaymentPlugin_Execute_SelectsTheMethod(t *testing.T) {
	for name, tc := range map[string]struct {
		methods  []string
		selected string
		want     string
		wantErr  string
	}{
		"selected":              {methods: []string{"bank_transfer", "card"}, selected: "card", want: "card"},
		"only method":           {methods: []string{"bank_transfer"}, want: "bank_transfer"},
		"none selected of many": {methods: []string{"bank_transfer", "card"}, wantErr: `"selected_method" is required: available methods are bank_transfer, card`},
		"selected cannot pay":   {methods: []string{"bank_transfer"}, selected: "card", wantErr: `payment method "card" cannot pay this payee`},
		"no method maps payee":  {methods: []string{}, wantErr: "no payment method can pay this payee"},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakePaymentService{methods: tc.methods, resp: checkoutResponse()}
			inputs := map[string]any{}
			if tc.selected != "" {
				inputs["selected_method"] = tc.selected
			}

			err := NewPaymentPlugin(svc).Execute(paymentPluginCtx(inputs), []byte(paymentConfigJSON))

			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.ErrorContains(t, err, "agency/app_fee", "the error names the payee")
				assert.False(t, svc.called)
				return
			}
			require.ErrorIs(t, err, ErrSuspended)
			assert.Equal(t, tc.want, svc.lastReq.GatewayID)
			assert.Equal(t, tc.want, svc.lastReq.Metadata["method_id"])
		})
	}
}

func TestPaymentPlugin_Execute_ListMethodsError(t *testing.T) {
	boom := errors.New("registry unavailable")
	svc := &fakePaymentService{listErr: boom, resp: checkoutResponse()}

	err := NewPaymentPlugin(svc).Execute(paymentPluginCtx(nil), []byte(paymentConfigJSON))

	require.ErrorIs(t, err, boom)
	assert.False(t, svc.called)
}

// A payee the selected method does not map comes back from the payment service
// as payment.ErrPayeeNotMapped, naming the payee.
func TestPaymentPlugin_Execute_PayeeNotMapped(t *testing.T) {
	svc := &fakePaymentService{err: fmt.Errorf("gateway bank_transfer, payee agency/app_fee: %w", payment.ErrPayeeNotMapped)}

	err := NewPaymentPlugin(svc).Execute(paymentPluginCtx(nil), []byte(paymentConfigJSON))

	require.ErrorIs(t, err, payment.ErrPayeeNotMapped)
	assert.ErrorContains(t, err, "agency/app_fee")
}

func TestPaymentPlugin_Execute_RejectsMalformedReference(t *testing.T) {
	for name, tc := range map[string]struct {
		reference string
		inputs    map[string]any
		want      string
	}{
		"no issuer":  {reference: `{"id_type":"fee_payment_ref"}`, want: "needs an issuer and an id_type"},
		"no id_type": {reference: `{"issuer":"AGENCY"}`, want: "needs an issuer and an id_type"},
		"empty param name": {
			reference: `{"issuer":"AGENCY","id_type":"fee_payment_ref","params":{"":"/exporter_id"}}`,
			want:      "params has an empty name",
		},
		"empty value name": {
			reference: `{"issuer":"AGENCY","id_type":"fee_payment_ref","values":{"":"01"}}`,
			want:      "values has an empty name",
		},
		"param in params and values": {
			reference: `{"issuer":"AGENCY","id_type":"fee_payment_ref","params":{"exporterId":"/exporter_id"},"values":{"exporterId":"0002"}}`,
			want:      `names "exporterId" in both params and values`,
		},
		"relative pointer": {
			reference: `{"issuer":"AGENCY","id_type":"fee_payment_ref","params":{"exporterId":"0/exporter_id"}}`,
			want:      "params.exporterId",
		},
		"param is not a string": {
			reference: `{"issuer":"AGENCY","id_type":"fee_payment_ref","params":{"exporterId":"/exporter_id"}}`,
			inputs:    map[string]any{"exporter_id": 2.0},
			want:      `param "exporterId" (/exporter_id) is a float64, not a string`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakePaymentService{resp: checkoutResponse()}
			config := `{"org_id":"agency","service_id":"app_fee","currency":"USD","amount":12500,"reference":` + tc.reference + `}`

			err := NewPaymentPlugin(svc).Execute(paymentPluginCtx(tc.inputs), []byte(config))

			require.ErrorContains(t, err, tc.want)
			assert.False(t, svc.called, "the checkout opens only with a valid reference format")
		})
	}
}

// A reference a transaction already holds reaches the step as
// payment.ErrDuplicateReference; the step's retry generates a new one.
func TestPaymentPlugin_Execute_DuplicateReference(t *testing.T) {
	svc := &fakePaymentService{err: fmt.Errorf("failed to persist transaction: %w", payment.ErrDuplicateReference)}

	err := NewPaymentPlugin(svc).Execute(paymentPluginCtx(nil), []byte(paymentConfigJSON))

	require.ErrorIs(t, err, payment.ErrDuplicateReference)
}

// Converting a float keeps every digit it was written with, so an amount
// with too many decimal places is refused rather than silently rounded.
func TestDecimalFromAny_KeepsFloatPrecision(t *testing.T) {
	got, err := decimalFromAny(4500.567)
	if err != nil {
		t.Fatalf("decimalFromAny(4500.567) unexpected error: %v", err)
	}
	if got.String() != "4500.567" {
		t.Fatalf("decimalFromAny(4500.567) = %s, want 4500.567", got.String())
	}
}

func TestNewPaymentPlugin_NilServicePanics(t *testing.T) {
	assert.Panics(t, func() { NewPaymentPlugin(nil) })
}
