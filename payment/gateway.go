package payment

import (
	"context"
	"encoding/json"
	"errors"
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
