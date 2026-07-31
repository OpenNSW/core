// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

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
	ctx := ContextWithRequest(r.Context(), r)

	resp, err := h.service.ValidateReference(ctx, gatewayID, body, r.Header)
	if err != nil {
		// Unverified caller: reject before any gateway-specific parsing or
		// disclosure of presentment info. 401, not the generic 500 below —
		// this is an authentication failure, not a transient/internal one.
		if errors.Is(err, ErrWebhookVerificationFailed) {
			slog.WarnContext(ctx, "validation request failed verification", "gateway", gatewayID, "error", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		slog.ErrorContext(ctx, "failed to validate reference", "gateway", gatewayID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if resp == nil {
		slog.ErrorContext(ctx, "validation response is nil", "gateway", gatewayID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.HTTPStatus)
	if _, err := w.Write(resp.Payload); err != nil {
		slog.ErrorContext(ctx, "failed to write response", "error", err)
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
	ctx := ContextWithRequest(r.Context(), r)

	resp, err := h.service.ProcessWebhook(ctx, gatewayID, body, r.Header)
	if err != nil {
		// Unverified caller: rejected before any parsing or settlement
		// happened. 401 (see rationale on the validate handler above),
		// distinct from the 404/400/422 business-logic cases and the
		// transient-500 fallback below.
		if errors.Is(err, ErrWebhookVerificationFailed) {
			slog.WarnContext(ctx, "webhook failed verification", "gateway", gatewayID, "error", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// An unknown reference is permanent; respond 404 so the gateway stops
		// retrying instead of hammering us forever. Everything else is treated
		// as transient (500) so the gateway's retry can re-drive it.
		if errors.Is(err, ErrTransactionNotFound) {
			slog.WarnContext(ctx, "webhook for unknown reference", "gateway", gatewayID, "error", err)
			http.Error(w, "unknown payment reference", http.StatusNotFound)
			return
		}
		// Unsupported status is a permanent payload problem; 400 so the gateway
		// stops retrying instead of hammering us with a body we can't process.
		if errors.Is(err, ErrUnsupportedWebhookStatus) {
			slog.WarnContext(ctx, "webhook with unsupported status", "gateway", gatewayID, "error", err)
			http.Error(w, "unsupported payment status", http.StatusBadRequest)
			return
		}
		// Amount/currency mismatch: never mark paid, and don't retry.
		if errors.Is(err, ErrAmountMismatch) {
			slog.WarnContext(ctx, "webhook amount/currency mismatch", "gateway", gatewayID, "error", err)
			http.Error(w, "payment amount mismatch", http.StatusUnprocessableEntity)
			return
		}
		slog.ErrorContext(ctx, "webhook processing failed", "gateway", gatewayID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if resp == nil {
		slog.ErrorContext(ctx, "webhook response is nil", "gateway", gatewayID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.HTTPStatus)
	if _, err := w.Write(resp.Payload); err != nil {
		slog.ErrorContext(ctx, "failed to write webhook response", "error", err)
	}
}
