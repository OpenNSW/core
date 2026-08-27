// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenNSW/core/shared/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockService implements PaymentService for handler-level tests.
type mockService struct {
	validateResp *ValidationResponse
	validateErr  error
	webhookResp  *WebhookResponse
	webhookErr   error
}

func (m *mockService) ListAvailableMethods(context.Context) ([]GatewayInfo, error) { return nil, nil }
func (m *mockService) CreateCheckoutSession(context.Context, CreateCheckoutRequest) (*CreateCheckoutResponse, error) {
	return nil, nil
}
func (m *mockService) ValidateReference(context.Context, string, json.RawMessage, map[string][]string) (*ValidationResponse, error) {
	return m.validateResp, m.validateErr
}
func (m *mockService) ProcessWebhook(context.Context, string, []byte, map[string][]string) (*WebhookResponse, error) {
	return m.webhookResp, m.webhookErr
}
func (m *mockService) SetTaskCompleter(TaskCompleter) {}
func (m *mockService) WithAuditor(audit.Auditor)      {}

// serve routes a webhook POST through a mux so PathValue("gatewayId") resolves.
func serveWebhook(svc PaymentService) *httptest.ResponseRecorder {
	h := NewHTTPHandler(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/payments/{gatewayId}/webhook", h.HandleWebhook)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/govpay/webhook", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestHandleWebhook_StatusClassification(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"success":             {err: nil, want: http.StatusOK},
		"verification failed": {err: fmt.Errorf("gw: %w (bad sig)", ErrWebhookVerificationFailed), want: http.StatusUnauthorized},
		"not found":           {err: fmt.Errorf("ref X: %w", ErrTransactionNotFound), want: http.StatusNotFound},
		"unsupported status":  {err: fmt.Errorf("bad: %w", ErrUnsupportedWebhookStatus), want: http.StatusBadRequest},
		"amount mismatch":     {err: fmt.Errorf("bad: %w", ErrAmountMismatch), want: http.StatusUnprocessableEntity},
		"transient":           {err: fmt.Errorf("db down"), want: http.StatusInternalServerError},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rr := serveWebhook(&mockService{
				webhookResp: &WebhookResponse{HTTPStatus: http.StatusOK, Payload: []byte(`{"message":"Success"}`)},
				webhookErr:  tc.err,
			})
			assert.Equal(t, tc.want, rr.Code)
		})
	}
}

func TestHandleValidateReference_WritesGatewayResponse(t *testing.T) {
	svc := &mockService{validateResp: &ValidationResponse{
		HTTPStatus: http.StatusOK,
		Payload:    []byte(`{"message":"Success"}`),
	}}
	h := NewHTTPHandler(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/payments/{gatewayId}/validate", h.HandleValidateReference)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/govpay/validate", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"message":"Success"}`, rr.Body.String())
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
}

func TestHandleValidateReference_ServiceErrorIs500(t *testing.T) {
	svc := &mockService{validateErr: fmt.Errorf("boom")}
	h := NewHTTPHandler(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/payments/{gatewayId}/validate", h.HandleValidateReference)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/govpay/validate", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestHandleValidateReference_VerificationFailureIs401(t *testing.T) {
	svc := &mockService{validateErr: fmt.Errorf("gw: %w (bad sig)", ErrWebhookVerificationFailed)}
	h := NewHTTPHandler(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/payments/{gatewayId}/validate", h.HandleValidateReference)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/govpay/validate", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// newRealServiceForGateway wires a real PaymentService (not the shallow
// mockService above) around a single registered gateway, for tests that
// need to prove behavior all the way through PaymentService into a
// PaymentGateway — e.g. that HTTPHandler's context enrichment actually
// reaches VerifyWebhook. repo is nil: safe here only because these tests
// drive gateway.VerifyWebhook to fail before any repository call happens.
func newRealServiceForGateway(t *testing.T, gatewayID string, gw PaymentGateway) PaymentService {
	t.Helper()
	path := writeTempConfig(t, fmt.Sprintf(`{"version":"1.0","methods":[{"id":%q,"is_active":true}]}`, gatewayID))
	factories := map[string]Factory{
		gatewayID: func(json.RawMessage) (PaymentGateway, error) { return gw, nil },
	}
	registry, err := NewRegistry(path, factories)
	require.NoError(t, err)
	return NewPaymentService(nil, registry)
}

func TestHandleWebhook_PopulatesRequestContextForVerifyWebhook(t *testing.T) {
	mockG := new(MockGateway)
	bodyBytes := []byte(`{"reference_number":"TNSWABCDEFGH"}`)

	mockG.On("VerifyWebhook", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ctx, ok := args.Get(0).(context.Context)
			require.True(t, ok)
			req := RequestFromContext(ctx)
			require.NotNil(t, req, "expected HTTPHandler to populate the request into ctx")
			assert.Equal(t, http.MethodPost, req.Method)
			assert.Equal(t, "/api/v1/payments/gw1/webhook", req.URL.Path)
			gotBody, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			assert.Empty(t, gotBody, "req.Body should already be drained by HTTPHandler — gateways must use the explicit body parameter for the payload")
		}).
		Return(fmt.Errorf("bad sig: %w", ErrWebhookVerificationFailed))

	svc := newRealServiceForGateway(t, "gw1", mockG)
	h := NewHTTPHandler(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/payments/{gatewayId}/webhook", h.HandleWebhook)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/gw1/webhook", bytes.NewReader(bodyBytes))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	mockG.AssertExpectations(t)
}

func TestHandleValidateReference_PopulatesRequestContextForVerifyWebhook(t *testing.T) {
	mockG := new(MockGateway)
	bodyBytes := []byte(`{"reference_number":"TNSWABCDEFGH"}`)

	mockG.On("VerifyWebhook", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ctx, ok := args.Get(0).(context.Context)
			require.True(t, ok)
			req := RequestFromContext(ctx)
			require.NotNil(t, req, "expected HTTPHandler to populate the request into ctx")
			assert.Equal(t, http.MethodPost, req.Method)
			assert.Equal(t, "/api/v1/payments/gw1/validate", req.URL.Path)
			gotBody, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			assert.Empty(t, gotBody, "req.Body should already be drained by HTTPHandler — gateways must use the explicit body parameter for the payload")
		}).
		Return(fmt.Errorf("bad sig: %w", ErrWebhookVerificationFailed))

	svc := newRealServiceForGateway(t, "gw1", mockG)
	h := NewHTTPHandler(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/payments/{gatewayId}/validate", h.HandleValidateReference)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/gw1/validate", bytes.NewReader(bodyBytes))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	mockG.AssertExpectations(t)
}

func TestHandleWebhook_MissingGatewayID(t *testing.T) {
	h := NewHTTPHandler(&mockService{})
	rr := httptest.NewRecorder()
	h.HandleWebhook(rr, httptest.NewRequest(http.MethodPost, "/x", http.NoBody))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleValidateReference_MissingGatewayID(t *testing.T) {
	h := NewHTTPHandler(&mockService{})
	rr := httptest.NewRecorder()
	h.HandleValidateReference(rr, httptest.NewRequest(http.MethodPost, "/x", http.NoBody))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
