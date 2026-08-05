// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

type InteractionType string

const (
	FlowTypeRedirect    InteractionType = "REDIRECT"
	FlowTypeInstruction InteractionType = "INSTRUCTION"
)

// WebhookStatus is the canonical, gateway-neutral outcome a gateway must
// normalize its own status vocabulary into when parsing a webhook.
type WebhookStatus string

const (
	WebhookStatusPending WebhookStatus = "PENDING"
	WebhookStatusSuccess WebhookStatus = "SUCCESS"
	WebhookStatusFailed  WebhookStatus = "FAILED"
)

// ErrUnsupportedWebhookStatus indicates a gateway status that could not be
// normalized into a WebhookStatus. It is a permanent condition (retrying the
// same payload won't help), so callers should not signal the gateway to retry.
var ErrUnsupportedWebhookStatus = errors.New("unsupported webhook status")

// ErrWebhookVerificationFailed indicates a caller — either a real-time
// validation request or an asynchronous webhook notification — could not be
// verified as genuinely originating from the gateway it claims to be. No
// transaction may be settled, and no presentment information may be
// disclosed, on the strength of an unverified caller, so this must be
// checked (and satisfied) before any gateway-specific parsing of the
// request runs.
var ErrWebhookVerificationFailed = errors.New("webhook verification failed")

// NewWebhookVerificationError builds the error a VerifyWebhook implementation
// should return once it has positively determined the caller is NOT
// genuinely this gateway (see VerifyWebhook's error-classification contract
// below). It wraps ErrWebhookVerificationFailed via %w, so
// errors.Is(err, ErrWebhookVerificationFailed) succeeds and HTTPHandler maps
// the response to 401.
//
// Using this instead of hand-rolling fmt.Errorf("%s: %w", reason,
// ErrWebhookVerificationFailed) is entirely optional — any error that
// already wraps ErrWebhookVerificationFailed some other way is equally
// valid — but it gives gateway authors an easy, hard-to-get-wrong default,
// reducing the risk of forgetting the %w and misclassifying a genuine
// rejection as an operational failure.
func NewWebhookVerificationError(reason string) error {
	return fmt.Errorf("%s: %w", reason, ErrWebhookVerificationFailed)
}

type SessionRequest struct {
	Amount             decimal.Decimal `json:"amount"`
	Currency           string          `json:"currency"`
	SuccessRedirectURL string          `json:"success_redirect_url"`
	CancelRedirectURL  string          `json:"cancel_redirect_url"`
}

type SessionResponse struct {
	SessionID    string          `json:"session_id"`
	Type         InteractionType `json:"type"`
	CheckoutURL  string          `json:"checkout_url,omitempty"`
	Instructions string          `json:"instructions,omitempty"`
}

// WebhookPayload represents the external callback from LankaPay to the Payment Service.
type WebhookPayload struct {
	ReferenceNumber      string            `json:"reference_number"`
	SessionID            string            `json:"session_id"`
	GatewayTransactionID string            `json:"gateway_transaction_id"`
	Status               WebhookStatus     `json:"status"`
	Amount               decimal.Decimal   `json:"amount"`
	Currency             string            `json:"currency"`
	PaymentMethod        string            `json:"payment_method"`
	Timestamp            string            `json:"timestamp"`
	Metadata             map[string]string `json:"metadata"`
}

// ValidationTransaction represents a minimal view of a payment transaction for validation purposes.
type ValidationTransaction struct {
	ReferenceNumber string            `json:"reference_number"`
	Amount          decimal.Decimal   `json:"amount"`
	Currency        string            `json:"currency"`
	Status          string            `json:"status"`
	ExpiryDate      time.Time         `json:"expiry_date"`
	Metadata        map[string]string `json:"metadata"`
}

// ValidationResponse represents a structured response for a validation request.
type ValidationResponse struct {
	Payload    json.RawMessage
	HTTPStatus int
}

// WebhookResponse is the gateway-specific acknowledgement returned to the
// gateway after a webhook (payment-completion) notification has been processed.
// For GovPay+ this carries the UpdateResponse (paymentData receipt).
type WebhookResponse struct {
	Payload    json.RawMessage
	HTTPStatus int
}

// Factory constructs a configured, ready-to-use gateway from its raw config.
// One factory per gateway type; the registry calls it once at init so gateways
// are immutable after construction (no post-init config mutation).
type Factory func(config json.RawMessage) (PaymentGateway, error)

// PaymentGateway defines the interface for external payment gateway integration.
type PaymentGateway interface {
	// GetFlowType returns the flow type of the gateway (REDIRECT or INSTRUCTION).
	GetFlowType() InteractionType

	// CreateSession initializes a payment session with the gateway.
	CreateSession(ctx context.Context, req SessionRequest) (*SessionResponse, error)

	// VerifyWebhook authenticates an inbound request — a real-time
	// validation request or an asynchronous webhook notification — as
	// genuinely originating from this gateway, using whatever scheme the
	// gateway requires (e.g. an HMAC signature over body, a bearer token
	// extracted from headers, an IP allowlist, or a server-side status
	// check against the gateway's own API — not every scheme needs to be
	// cryptographic). It is called before ExtractReferenceNumber and
	// ParseWebhook, and a non-nil return blocks both: no reference lookup, no
	// settlement, and no presentment info may reach an unverified caller.
	// There is no default/no-op — every implementation must perform a real
	// check.
	//
	// Error contract: return ErrWebhookVerificationFailed (wrapped via %w)
	// only when verification has positively determined the caller is NOT
	// genuinely this gateway (e.g. an invalid signature, an expired or
	// unrecognized token). HTTPHandler maps that sentinel to 401. Return any
	// other error for a failure to complete verification for an operational
	// reason (a timeout reaching an upstream introspection/JWKS endpoint, a
	// missing local configuration, a cancelled context) — those are NOT proof
	// the caller is invalid and must not use this sentinel; they are treated
	// as transient (mapped to 500, so the gateway's retry can re-drive it),
	// exactly like an unclassified error from any of this interface's other
	// methods. Forgetting to wrap the sentinel for a genuine rejection has a
	// concrete cost: HTTPHandler responds 500 instead of 401, so the caller
	// burns its retry budget retrying a request that can never succeed, and
	// monitoring records it as a transient/internal failure rather than an
	// auth rejection. See NewWebhookVerificationError for a helper that
	// builds a correctly-wrapped rejection.
	//
	// For a scheme needing anything beyond body/headers — e.g. query
	// parameters, HTTP method, request path, TLS connection state, or remote
	// address — see RequestFromContext, which HTTPHandler populates with the
	// full inbound *http.Request before this is invoked.
	VerifyWebhook(ctx context.Context, body []byte, headers map[string][]string) error

	// ExtractReferenceNumber parses the gateway-specific validation request to extract the reference number.
	ExtractReferenceNumber(ctx context.Context, reqData json.RawMessage) (string, error)

	// HandleValidateReference formats the gateway-specific validation response.
	// tx is nil when no matching transaction exists (unknown reference or a
	// mismatched gateway); isPayable is the domain decision (exists, owned by
	// this gateway, pending, and not expired) the gateway should reflect back.
	HandleValidateReference(ctx context.Context, tx *ValidationTransaction, isPayable bool, reqData json.RawMessage) (*ValidationResponse, error)

	// ParseWebhook processes raw gateway notifications into a domain-neutral
	// payload (for the service to act on) together with the gateway-specific
	// acknowledgement to relay back once the notification has been accepted.
	ParseWebhook(ctx context.Context, body []byte, headers map[string][]string) (*WebhookPayload, *WebhookResponse, error)
}
