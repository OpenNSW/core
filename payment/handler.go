package payment

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
)

// HTTPHandler handles public HTTP requests for the Payment Service.
type HTTPHandler struct {
	service PaymentService
}

// NewHTTPHandler creates a new handler.
func NewHTTPHandler(service PaymentService) *HTTPHandler {
	return &HTTPHandler{service: service}
}

// HandleValidateReference handles POST /api/v1/payments/:gatewayId/validate
// Called by gateways to query if a reference number is valid and payable.
func (h *HTTPHandler) HandleValidateReference(w http.ResponseWriter, r *http.Request) {
	gatewayID := r.PathValue("gatewayId")
	if gatewayID == "" {
		http.Error(w, "gateway ID is required in URL", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
		return
	}

	resp, err := h.service.ValidateReference(r.Context(), gatewayID, body)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to validate reference", "gateway", gatewayID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if resp == nil {
		slog.ErrorContext(r.Context(), "validation response is nil", "gateway", gatewayID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.HTTPStatus)
	if _, err := w.Write(resp.Payload); err != nil {
		slog.ErrorContext(r.Context(), "failed to write response", "error", err)
	}
}

// HandleWebhook handles POST /api/v1/payments/:gatewayID/webhook
// Called by payment gateways to notify about payment successes and failures.
func (h *HTTPHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	gatewayID := r.PathValue("gatewayId")
	if gatewayID == "" {
		http.Error(w, "gateway ID is required in URL", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
		return
	}

	err = h.service.ProcessWebhook(r.Context(), gatewayID, body, r.Header)
	if err != nil {
		// An unknown reference is permanent; respond 404 so the gateway stops
		// retrying instead of hammering us forever. Everything else is treated
		// as transient (500) so the gateway's retry can re-drive it.
		if errors.Is(err, ErrTransactionNotFound) {
			slog.WarnContext(r.Context(), "webhook for unknown reference", "gateway", gatewayID, "error", err)
			http.Error(w, "unknown payment reference", http.StatusNotFound)
			return
		}
		// Unsupported status is a permanent payload problem; 400 so the gateway
		// stops retrying instead of hammering us with a body we can't process.
		if errors.Is(err, ErrUnsupportedWebhookStatus) {
			slog.WarnContext(r.Context(), "webhook with unsupported status", "gateway", gatewayID, "error", err)
			http.Error(w, "unsupported payment status", http.StatusBadRequest)
			return
		}
		// Amount/currency mismatch: never mark paid, and don't retry.
		if errors.Is(err, ErrAmountMismatch) {
			slog.WarnContext(r.Context(), "webhook amount/currency mismatch", "gateway", gatewayID, "error", err)
			http.Error(w, "payment amount mismatch", http.StatusUnprocessableEntity)
			return
		}
		slog.ErrorContext(r.Context(), "webhook processing failed", "gateway", gatewayID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status": "accepted"}`))
}
