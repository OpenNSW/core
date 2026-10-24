// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/OpenNSW/core/payment"
	"github.com/OpenNSW/core/taskflow/callbacktoken"
	"github.com/OpenNSW/core/taskflow/store"
)

// fakeCheckouts offers methods for any payee and records the checkouts it is asked for.
type fakeCheckouts struct {
	methods   []string
	listErr   error
	createErr error

	listedFor []payment.Payee
	created   []payment.CreateCheckoutRequest
}

func (f *fakeCheckouts) ListAvailableMethods(_ context.Context, payee payment.Payee) ([]payment.GatewayInfo, error) {
	f.listedFor = append(f.listedFor, payee)
	infos := make([]payment.GatewayInfo, 0, len(f.methods))
	for _, id := range f.methods {
		infos = append(infos, payment.GatewayInfo{ID: id, IsActive: true})
	}
	return infos, f.listErr
}

func (f *fakeCheckouts) CreateCheckoutSession(_ context.Context, req payment.CreateCheckoutRequest) (*payment.CreateCheckoutResponse, error) {
	f.created = append(f.created, req)
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &payment.CreateCheckoutResponse{
		ReferenceNumber: "REF-1",
		SessionID:       "sess-1",
		Type:            payment.FlowTypeInstruction,
		Instructions:    "pay with REF-1",
	}, nil
}

const paymentStepConfig = `{"org_id":"org_a","service_id":"certificate_fee","service_name":"Certificate Fee","amount":"2000.00","currency":"LKR"}`

func paymentCtx(inputs map[string]any) PluginContext {
	return PluginContext{
		Context:         context.Background(),
		Record:          &store.TaskRecord{TaskID: testTaskID, ActiveStepID: testStepID},
		Inputs:          inputs,
		OutputNamespace: "payment",
	}
}

func TestPaymentPlugin_CreatesCheckoutForThePayee(t *testing.T) {
	svc := &fakeCheckouts{methods: []string{"govpay", "card"}}
	ctx := paymentCtx(map[string]any{"selected_method": "card"})

	err := NewPaymentPlugin(svc).Execute(ctx, []byte(paymentStepConfig))

	if !errors.Is(err, ErrSuspended) {
		t.Fatalf("Execute() = %v, want ErrSuspended", err)
	}
	wantPayee := payment.Payee{OrgID: "org_a", ServiceID: "certificate_fee"}
	if len(svc.listedFor) != 1 || svc.listedFor[0] != wantPayee {
		t.Errorf("methods listed for %v, want %v", svc.listedFor, wantPayee)
	}
	if len(svc.created) != 1 {
		t.Fatalf("created %d checkouts, want 1", len(svc.created))
	}
	req := svc.created[0]
	if req.GatewayID != "card" || req.Payee != wantPayee || req.Currency != "LKR" ||
		!req.Amount.Equal(decimal.RequireFromString("2000")) {
		t.Errorf("checkout = %+v", req)
	}
	if until := time.Until(req.ExpiresAt); until < 23*time.Hour || until > 24*time.Hour {
		t.Errorf("checkout expires in %s, want 24h", until)
	}
	if task, step, err := callbacktoken.Decode(req.CallbackToken); err != nil || task != testTaskID || step != testStepID {
		t.Errorf("callback token names (%s, %s, %v), want the active step", task, step, err)
	}
	if ctx.Record.State != StatePendingPayment {
		t.Errorf("state = %q, want %q", ctx.Record.State, StatePendingPayment)
	}
	out, _ := ctx.Record.Data["payment"].(map[string]any)
	for key, want := range map[string]any{
		"reference_number": "REF-1",
		"selected_method":  "card",
		"amount":           "2000",
		"flow_type":        "INSTRUCTION",
		"instructions":     "pay with REF-1",
		"service_name":     "Certificate Fee",
		"org_id":           "org_a",
		"service_id":       "certificate_fee",
	} {
		if out[key] != want {
			t.Errorf("output %s = %v, want %v", key, out[key], want)
		}
	}
}

// With no selection, the only method that can pay the payee is used; with several, the payer
// has to choose.
func TestPaymentPlugin_MethodSelection(t *testing.T) {
	for name, tc := range map[string]struct {
		methods  []string
		selected any
		want     string
		wantErr  string
	}{
		"selected":                {methods: []string{"govpay", "card"}, selected: "govpay", want: "govpay"},
		"only method":             {methods: []string{"govpay"}, want: "govpay"},
		"none selected of many":   {methods: []string{"govpay", "card"}, wantErr: `"selected_method" is required: available methods are govpay, card`},
		"selected cannot pay":     {methods: []string{"govpay"}, selected: "card", wantErr: `"card" cannot pay this payee`},
		"no method for payee":     {wantErr: "no payment method can pay this payee"},
		"blank selection is none": {methods: []string{"govpay"}, selected: "  ", want: "govpay"},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeCheckouts{methods: tc.methods}
			inputs := map[string]any{}
			if tc.selected != nil {
				inputs["selected_method"] = tc.selected
			}

			err := NewPaymentPlugin(svc).Execute(paymentCtx(inputs), []byte(paymentStepConfig))

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Execute() = %v, want error containing %q", err, tc.wantErr)
				}
				if len(svc.created) != 0 {
					t.Error("no checkout may be created when no method can be chosen")
				}
				return
			}
			if !errors.Is(err, ErrSuspended) {
				t.Fatalf("Execute() = %v, want ErrSuspended", err)
			}
			if got := svc.created[0].GatewayID; got != tc.want {
				t.Errorf("method = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPaymentPlugin_Amount(t *testing.T) {
	for name, tc := range map[string]struct {
		input   any
		config  string
		want    string
		wantErr string
	}{
		"configured":             {config: `"2000.00"`, want: "2000"},
		"input number wins":      {input: 4500.5, config: `"2000.00"`, want: "4500.5"},
		"input string wins":      {input: "120.25", config: `"2000.00"`, want: "120.25"},
		"input json.Number":      {input: json.Number("75"), want: "75"},
		"missing":                {wantErr: "amount is required"},
		"negative input":         {input: -5.0, config: `"2000.00"`, wantErr: "must be positive"},
		"unparseable input":      {input: "lots", config: `"2000.00"`, wantErr: `input "amount" is invalid`},
		"unsupported input type": {input: true, wantErr: "unsupported type bool"},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeCheckouts{methods: []string{"govpay"}}
			config := `{"org_id":"org_a","service_id":"fee","currency":"LKR"`
			if tc.config != "" {
				config += `,"amount":` + tc.config
			}
			config += `}`
			inputs := map[string]any{}
			if tc.input != nil {
				inputs["amount"] = tc.input
			}

			err := NewPaymentPlugin(svc).Execute(paymentCtx(inputs), []byte(config))

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Execute() = %v, want error containing %q", err, tc.wantErr)
				}
				return
			}
			if !errors.Is(err, ErrSuspended) {
				t.Fatalf("Execute() = %v, want ErrSuspended", err)
			}
			if got := svc.created[0].Amount; !got.Equal(decimal.RequireFromString(tc.want)) {
				t.Errorf("amount = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestPaymentPlugin_InvalidConfig(t *testing.T) {
	for name, config := range map[string]string{
		"not json":         `{`,
		"no org_id":        `{"service_id":"fee","amount":"1","currency":"LKR"}`,
		"blank service_id": `{"org_id":"org_a","service_id":" ","amount":"1","currency":"LKR"}`,
		"no currency":      `{"org_id":"org_a","service_id":"fee","amount":"1"}`,
		"bad expires_in":   `{"org_id":"org_a","service_id":"fee","amount":"1","currency":"LKR","expires_in":"soon"}`,
		"zero expires_in":  `{"org_id":"org_a","service_id":"fee","amount":"1","currency":"LKR","expires_in":"0s"}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeCheckouts{methods: []string{"govpay"}}
			ctx := paymentCtx(nil)

			err := NewPaymentPlugin(svc).Execute(ctx, []byte(config))

			if err == nil || errors.Is(err, ErrSuspended) {
				t.Fatalf("Execute() = %v, want a config error", err)
			}
			if len(svc.created) != 0 || ctx.Record.State != "" {
				t.Error("a bad config must not create a checkout or move the record")
			}
		})
	}
}

func TestPaymentPlugin_ExpiresIn(t *testing.T) {
	svc := &fakeCheckouts{methods: []string{"govpay"}}
	config := `{"org_id":"org_a","service_id":"fee","amount":"1","currency":"LKR","expires_in":"2h"}`

	if err := NewPaymentPlugin(svc).Execute(paymentCtx(nil), []byte(config)); !errors.Is(err, ErrSuspended) {
		t.Fatalf("Execute() = %v, want ErrSuspended", err)
	}
	if until := time.Until(svc.created[0].ExpiresAt); until < time.Hour || until > 2*time.Hour {
		t.Errorf("checkout expires in %s, want 2h", until)
	}
}

// The step's static metadata and what the host derives from the step both reach the checkout;
// a key set by both is refused rather than one silently winning.
func TestPaymentPlugin_Metadata(t *testing.T) {
	derive := WithMetadata(func(ctx PluginContext, config json.RawMessage) (map[string]string, error) {
		var c struct {
			Reference struct {
				Issuer string `json:"issuer"`
			} `json:"reference"`
		}
		if err := json.Unmarshal(config, &c); err != nil {
			return nil, err
		}
		exporter, _ := ctx.Inputs["exporter_id"].(string)
		return map[string]string{"reference_issuer": c.Reference.Issuer, "reference_param.exporter": exporter}, nil
	})

	t.Run("merged", func(t *testing.T) {
		svc := &fakeCheckouts{methods: []string{"govpay"}}
		config := `{"org_id":"org_a","service_id":"fee","amount":"1","currency":"LKR",
			"metadata":{"channel":"portal"},"reference":{"issuer":"ACME"}}`

		err := NewPaymentPlugin(svc, derive).Execute(paymentCtx(map[string]any{"exporter_id": "0002"}), []byte(config))

		if !errors.Is(err, ErrSuspended) {
			t.Fatalf("Execute() = %v, want ErrSuspended", err)
		}
		got := svc.created[0].Metadata
		want := map[string]string{"channel": "portal", "reference_issuer": "ACME", "reference_param.exporter": "0002"}
		if len(got) != len(want) {
			t.Fatalf("metadata = %v, want %v", got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("metadata[%s] = %q, want %q", k, got[k], v)
			}
		}
	})

	t.Run("clash", func(t *testing.T) {
		svc := &fakeCheckouts{methods: []string{"govpay"}}
		config := `{"org_id":"org_a","service_id":"fee","amount":"1","currency":"LKR","metadata":{"reference_issuer":"X"}}`

		err := NewPaymentPlugin(svc, derive).Execute(paymentCtx(nil), []byte(config))

		if err == nil || !strings.Contains(err.Error(), `"reference_issuer" is set by both`) {
			t.Fatalf("Execute() = %v, want a clash error", err)
		}
		if len(svc.created) != 0 {
			t.Error("no checkout may be created on a metadata clash")
		}
	})

	t.Run("host error", func(t *testing.T) {
		svc := &fakeCheckouts{methods: []string{"govpay"}}
		sentinel := errors.New("unknown reference format")
		failing := WithMetadata(func(PluginContext, json.RawMessage) (map[string]string, error) { return nil, sentinel })

		err := NewPaymentPlugin(svc, failing).Execute(paymentCtx(nil), []byte(paymentStepConfig))

		if !errors.Is(err, sentinel) {
			t.Fatalf("Execute() = %v, want %v", err, sentinel)
		}
	})
}

// A failed checkout names the payee and method, leaves the record where it was and is not
// reported as a suspension.
func TestPaymentPlugin_ServiceErrors(t *testing.T) {
	notMapped := &fakeCheckouts{methods: []string{"govpay"}, createErr: payment.ErrPayeeNotMapped}
	ctx := paymentCtx(nil)
	err := NewPaymentPlugin(notMapped).Execute(ctx, []byte(paymentStepConfig))
	if !errors.Is(err, payment.ErrPayeeNotMapped) || !strings.Contains(err.Error(), "org_a/certificate_fee") || !strings.Contains(err.Error(), "govpay") {
		t.Errorf("Execute() = %v, want ErrPayeeNotMapped naming the payee and method", err)
	}
	if ctx.Record.State != "" || ctx.Record.Data != nil {
		t.Error("a failed checkout must not move the record or write output")
	}

	listFails := &fakeCheckouts{listErr: errors.New("registry down")}
	if err := NewPaymentPlugin(listFails).Execute(paymentCtx(nil), []byte(paymentStepConfig)); err == nil || errors.Is(err, ErrSuspended) {
		t.Errorf("Execute() = %v, want the listing error", err)
	}
}

func TestPaymentPlugin_RequiresRecordAndService(t *testing.T) {
	if err := NewPaymentPlugin(nil).Execute(paymentCtx(nil), []byte(paymentStepConfig)); err == nil {
		t.Error("expected an error without a checkout service")
	}
	ctx := paymentCtx(nil)
	ctx.Record = nil
	if err := NewPaymentPlugin(&fakeCheckouts{}).Execute(ctx, []byte(paymentStepConfig)); err == nil {
		t.Error("expected an error without a task record")
	}
}
