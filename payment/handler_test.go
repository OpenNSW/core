package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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
func (m *mockService) ValidateReference(context.Context, string, json.RawMessage) (*ValidationResponse, error) {
	return m.validateResp, m.validateErr
}
func (m *mockService) ProcessWebhook(context.Context, string, []byte, map[string][]string) (*WebhookResponse, error) {
	return m.webhookResp, m.webhookErr
}
func (m *mockService) SetTaskCompleter(TaskCompleter) {}

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
		"success":            {err: nil, want: http.StatusOK},
		"not found":          {err: fmt.Errorf("ref X: %w", ErrTransactionNotFound), want: http.StatusNotFound},
		"unsupported status": {err: fmt.Errorf("bad: %w", ErrUnsupportedWebhookStatus), want: http.StatusBadRequest},
		"amount mismatch":    {err: fmt.Errorf("bad: %w", ErrAmountMismatch), want: http.StatusUnprocessableEntity},
		"transient":          {err: fmt.Errorf("db down"), want: http.StatusInternalServerError},
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
