// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrTransactionNotFound indicates no payment transaction matches a given
// reference. This is a permanent condition, so callers (e.g. the webhook
// handler) should not ask the gateway to retry.
var ErrTransactionNotFound = errors.New("payment transaction not found")

// ErrAmountMismatch indicates a successful-payment webhook reported an amount or
// currency that doesn't match the recorded transaction. Permanent and
// suspicious, so it is never marked paid and the gateway should not retry.
var ErrAmountMismatch = errors.New("webhook amount/currency mismatch")

// toDomainStatus maps a canonical gateway WebhookStatus onto the internal
// PaymentStatus. It is total over the known statuses and rejects anything else
// with gateways.ErrUnsupportedWebhookStatus so a bad value can't reach the DB.
func toDomainStatus(s WebhookStatus) (PaymentStatus, error) {
	switch s {
	case WebhookStatusPending:
		return PaymentStatusPending, nil
	case WebhookStatusSuccess:
		return PaymentStatusSuccess, nil
	case WebhookStatusFailed:
		return PaymentStatusFailed, nil
	default:
		return "", fmt.Errorf("status %q: %w", s, ErrUnsupportedWebhookStatus)
	}
}

// verifyCaller runs the gateway's cryptographic verification hook before
// any gateway-specific parsing. It does NOT unconditionally classify a
// failure as an authentication rejection: per VerifyWebhook's error
// contract, the gateway itself wraps ErrWebhookVerificationFailed only
// when it has positively determined the caller is invalid, and
// errors.Is(..., ErrWebhookVerificationFailed) downstream only succeeds
// for that case. Any other error (an operational failure inside the
// gateway's own verification logic) propagates here unwrapped-by-us, so
// it falls through to the generic transient/500 handling in HTTPHandler,
// not a 401.
func verifyCaller(ctx context.Context, gateway PaymentGateway, gatewayID string, body []byte, headers map[string][]string) error {
	if err := gateway.VerifyWebhook(ctx, body, headers); err != nil {
		return fmt.Errorf("gateway %s: %w", gatewayID, err)
	}
	return nil
}

// TaskCompleter resumes a suspended workflow step once a payment reaches a
// terminal outcome. It is satisfied by the taskv2 TaskManager.
type TaskCompleter interface {
	CompleteTaskStep(ctx context.Context, taskID string, payload map[string]any) error
}

// PaymentService defines the high-level orchestration for payments.
type PaymentService interface {
	// ListAvailableMethods returns the rendering information for all active payment gateways.
	ListAvailableMethods(ctx context.Context) ([]GatewayInfo, error)

	// CreateCheckoutSession initializes a payment session and generates a ReferenceNumber.
	CreateCheckoutSession(ctx context.Context, req CreateCheckoutRequest) (*CreateCheckoutResponse, error)

	// ValidateReference is used for real-time validation requests from gateways.
	ValidateReference(ctx context.Context, gatewayID string, rawBody json.RawMessage, headers map[string][]string) (*ValidationResponse, error)

	// ProcessWebhook handles asynchronous notifications from payment gateways and
	// returns the gateway-specific acknowledgement to relay back to the gateway.
	ProcessWebhook(ctx context.Context, gatewayID string, body []byte, headers map[string][]string) (*WebhookResponse, error)

	// SetTaskCompleter injects the dependency used to advance the workflow when
	// a payment settles. Wired post-construction to avoid an import cycle with taskv2.
	SetTaskCompleter(completer TaskCompleter)

	// SetAuditor injects an optional auditor for recording payment audit events.
	SetAuditor(auditor Auditor)
}

type paymentService struct {
	repo          PaymentRepository
	registry      GatewayRegistry
	taskCompleter TaskCompleter
	auditor       Auditor
}

// NewPaymentService initializes a new payment service.
func NewPaymentService(repo PaymentRepository, registry GatewayRegistry) PaymentService {
	return &paymentService{
		repo:     repo,
		registry: registry,
	}
}

func (s *paymentService) SetTaskCompleter(completer TaskCompleter) {
	s.taskCompleter = completer
}

// SetAuditor injects an optional auditor for recording payment audit events.
// Passing nil disables auditing.
func (s *paymentService) SetAuditor(auditor Auditor) {
	s.auditor = auditor
}

// auditPayment is a nil-safe helper that emits a payment audit event.
func (s *paymentService) auditPayment(ctx context.Context, e AuditEvent) {
	if s.auditor != nil {
		s.auditor.AuditPayment(ctx, e)
	}
}

func (s *paymentService) ListAvailableMethods(ctx context.Context) ([]GatewayInfo, error) {
	return s.registry.ListInfo(), nil
}

const referenceCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// generatePaymentReference returns a non-guessable NSW reference of the form
// TNSWXXXXXXXX using crypto-grade randomness. Mirrors the generator in
// internal/payments.
func generatePaymentReference() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate random bytes: %v", err))
	}
	for i := range b {
		b[i] = referenceCharset[int(b[i])%len(referenceCharset)]
	}
	return fmt.Sprintf("TNSW%s", string(b))
}

func (s *paymentService) CreateCheckoutSession(ctx context.Context, req CreateCheckoutRequest) (*CreateCheckoutResponse, error) {
	now := time.Now()
	if err := req.validate(now); err != nil {
		return nil, fmt.Errorf("invalid checkout request: %w", err)
	}

	gateway, err := s.registry.Get(req.GatewayID)
	if err != nil {
		return nil, fmt.Errorf("failed to get gateway %s: %w", req.GatewayID, err)
	}

	// Let the gateway reject metadata it cannot operate without before any
	// state is created: no reference is burned and no PENDING row has to be
	// walked back to FAILED for what is purely a configuration error.
	if err := gateway.ValidateMetadata(req.Metadata); err != nil {
		return nil, fmt.Errorf("gateway %s rejected checkout metadata: %w", req.GatewayID, err)
	}

	taskID := req.Metadata["task_id"] // presence validated above

	// 1. Generate a unique NSW ReferenceNumber, retrying on the rare collision.
	var generatedRef string
	const maxRetries = 10
	for i := 0; i < maxRetries; i++ {
		candidate := generatePaymentReference()
		existing, err := s.repo.GetByReferenceNumber(ctx, candidate)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing reference number: %w", err)
		}
		if existing == nil {
			generatedRef = candidate
			break
		}
	}
	if generatedRef == "" {
		return nil, fmt.Errorf("failed to generate a unique payment reference after %d attempts", maxRetries)
	}

	// 2. Write-ahead: persist a PENDING transaction BEFORE contacting the gateway.
	// If the DB write fails we never create a gateway session (no orphan), and if
	// an inbound validation/webhook races the response it can still find the row.
	tx := &PaymentTransaction{
		ID:              uuid.NewString(),
		ReferenceNumber: generatedRef,
		TaskID:          taskID,
		GatewayID:       req.GatewayID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		Status:          PaymentStatusPending,
		ExpiryDate:      req.ExpiresAt,
		GatewayMetadata: req.Metadata,
	}
	if err := s.repo.Create(ctx, tx); err != nil {
		return nil, fmt.Errorf("failed to persist transaction: %w", err)
	}

	// 3. Initialize the session with the gateway.
	sessionReq := SessionRequest{
		Amount:             req.Amount,
		Currency:           req.Currency,
		SuccessRedirectURL: req.SuccessRedirectURL,
		CancelRedirectURL:  req.CancelRedirectURL,
		Metadata:           req.Metadata,
	}
	sessionResp, err := gateway.CreateSession(ctx, sessionReq)
	if err != nil {
		// Don't leave the row dangling as PENDING; mark it FAILED so a later
		// webhook or reconciliation can't treat an uninitialized session as payable.
		tx.Status = PaymentStatusFailed
		if uerr := s.repo.Update(ctx, tx); uerr != nil {
			slog.ErrorContext(ctx, "payment: failed to mark transaction failed after gateway error",
				"reference", tx.ReferenceNumber, "error", uerr)
		}
		return nil, fmt.Errorf("gateway failed to create session: %w", err)
	}

	// 4. Persist the gateway-assigned session id.
	if sessionResp != nil && sessionResp.SessionID != "" {
		tx.SessionID = sessionResp.SessionID
		if err := s.repo.Update(ctx, tx); err != nil {
			return nil, fmt.Errorf("failed to persist session id: %w", err)
		}
	}

	if sessionResp == nil {
		return nil, errors.New("gateway returned nil session response")
	}

	return &CreateCheckoutResponse{
		ReferenceNumber: generatedRef,
		SessionID:       sessionResp.SessionID,
		Type:            sessionResp.Type,
		CheckoutURL:     sessionResp.CheckoutURL,
		Instructions:    sessionResp.Instructions,
		ExpiresIn:       int(req.ExpiresAt.Sub(now).Seconds()),
	}, nil
}

func (s *paymentService) ValidateReference(ctx context.Context, gatewayID string, rawBody json.RawMessage, headers map[string][]string) (*ValidationResponse, error) {
	slog.DebugContext(ctx, "validating incoming payment reference", "gateway", gatewayID)

	// 1. Get the gateway from the registry using the ID from the URL
	gateway, err := s.registry.Get(gatewayID)
	if err != nil {
		err = fmt.Errorf("gateway %s not found: %w", gatewayID, err)
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionValidate, GatewayID: gatewayID,
			Failure: true, Error: err.Error(),
		})
		return nil, err
	}

	// 2. Verify the caller before any gateway-specific parsing runs. No
	// reference lookup, and no presentment info, may be disclosed to an
	// unverified caller.
	if err := verifyCaller(ctx, gateway, gatewayID, rawBody, headers); err != nil {
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionValidate, GatewayID: gatewayID,
			Failure: true, Error: err.Error(),
		})
		return nil, err
	}

	// 3. Extract reference number from raw body
	refNo, err := gateway.ExtractReferenceNumber(ctx, rawBody)
	if err != nil {
		err = fmt.Errorf("failed to extract reference number: %w", err)
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionValidate, GatewayID: gatewayID,
			Failure: true, Error: err.Error(),
		})
		return nil, err
	}

	// 4. Look up the transaction metadata from the DB
	tx, err := s.repo.GetByReferenceNumber(ctx, refNo)
	if err != nil {
		err = fmt.Errorf("failed to retrieve payment reference: %w", err)
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionValidate, GatewayID: gatewayID,
			Reference: refNo, Failure: true, Error: err.Error(),
		})
		return nil, err
	}

	// 5. Map the internal record to the gateway DTO and decide payability.
	// A nil validationTx signals "no usable transaction" to the gateway: either
	// the reference is unknown, or it belongs to a different gateway (we must not
	// leak one gateway's transaction details to another).
	var validationTx *ValidationTransaction
	isPayable := false
	if tx != nil {
		if tx.GatewayID != gatewayID {
			slog.WarnContext(ctx, "gateway mismatch during validation", "expected", tx.GatewayID, "received", gatewayID, "reference", tx.ReferenceNumber)
		} else {
			validationTx = &ValidationTransaction{
				ReferenceNumber: tx.ReferenceNumber,
				Amount:          tx.Amount,
				Currency:        tx.Currency,
				Status:          string(tx.Status),
				ExpiryDate:      tx.ExpiryDate,
				Metadata:        tx.GatewayMetadata,
			}
			// Payable = exists, owned by this gateway, still pending, not expired.
			isPayable = tx.Status == PaymentStatusPending && time.Now().Before(tx.ExpiryDate)
		}
	}

	// Domain status is known once the transaction has been mapped, including on
	// subsequent gateway formatting failures. Leave it empty only when there is
	// no usable domain transaction (unknown reference or gateway mismatch).
	status := ""
	if validationTx != nil {
		status = validationTx.Status
	}

	// 5. Delegate the protocol-specific response formatting to the gateway.
	resp, err := gateway.HandleValidateReference(ctx, validationTx, isPayable, rawBody)
	if err != nil {
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionValidate, GatewayID: gatewayID,
			Reference: refNo, Status: status, Failure: true, Error: err.Error(),
		})
		return nil, err
	}
	if resp == nil {
		err = errors.New("gateway returned nil validation response")
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionValidate, GatewayID: gatewayID,
			Reference: refNo, Status: status, Failure: true, Error: err.Error(),
		})
		return nil, err
	}
	s.auditPayment(ctx, AuditEvent{
		Action: AuditActionValidate, GatewayID: gatewayID, Reference: refNo, Status: status,
	})
	return resp, nil
}

func (s *paymentService) ProcessWebhook(ctx context.Context, gatewayID string, body []byte, headers map[string][]string) (*WebhookResponse, error) {
	gateway, err := s.registry.Get(gatewayID)
	if err != nil {
		err = fmt.Errorf("failed to get gateway %s: %w", gatewayID, err)
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionWebhook, GatewayID: gatewayID,
			Failure: true, Error: err.Error(),
		})
		return nil, err
	}

	// Verify the caller before any gateway-specific parsing runs. No
	// transaction may be settled on the strength of an unverified caller.
	if err := verifyCaller(ctx, gateway, gatewayID, body, headers); err != nil {
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionWebhook, GatewayID: gatewayID,
			Failure: true, Error: err.Error(),
		})
		return nil, err
	}

	gwPayload, webhookResp, err := gateway.ParseWebhook(ctx, body, headers)
	if err != nil {
		err = fmt.Errorf("gateway failed to parse webhook: %w", err)
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionWebhook, GatewayID: gatewayID,
			Failure: true, Error: err.Error(),
		})
		return nil, err
	}

	if gwPayload == nil {
		err = fmt.Errorf("gateway returned nil webhook payload")
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionWebhook, GatewayID: gatewayID,
			Failure: true, Error: err.Error(),
		})
		return nil, err
	}

	// Translate the canonical gateway status into our domain status, rejecting
	// anything unrecognized (defense-in-depth against a misbehaving gateway).
	newStatus, err := toDomainStatus(gwPayload.Status)
	if err != nil {
		err = fmt.Errorf("webhook for %s: %w", gwPayload.ReferenceNumber, err)
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionWebhook, GatewayID: gatewayID,
			Reference: gwPayload.ReferenceNumber, Failure: true, Error: err.Error(),
		})
		return nil, err
	}

	// Claim and apply the status transition atomically. A row-level lock makes
	// concurrent deliveries serialize on the record, so only the first one past
	// PENDING updates it and earns the right to advance the workflow.
	var (
		advance     bool
		advanceTask string
		finalStatus PaymentStatus
		refNum      string
		amount      string
		currency    string
	)

	err = s.repo.RunInTransaction(ctx, func(repo PaymentRepository) error {
		tx, err := repo.GetByReferenceNumberForUpdate(ctx, gwPayload.ReferenceNumber)
		if err != nil {
			return fmt.Errorf("failed to retrieve transaction by reference: %w", err)
		}
		if tx == nil {
			return fmt.Errorf("reference %s: %w", gwPayload.ReferenceNumber, ErrTransactionNotFound)
		}

		// Idempotency: a terminal status is already recorded (possibly by a
		// concurrent delivery that committed first) — nothing more to do.
		if tx.Status == PaymentStatusSuccess || tx.Status == PaymentStatusFailed {
			slog.InfoContext(ctx, "webhook ignored (idempotent)", "reference", tx.ReferenceNumber, "current_status", tx.Status)
			finalStatus = tx.Status
			return nil
		}

		// Before accepting a payment as settled, verify the gateway-reported
		// amount and currency match what we recorded at checkout. Reject on any
		// mismatch (incl. a missing amount) rather than marking it paid.
		if newStatus == PaymentStatusSuccess {
			if !gwPayload.Amount.Equal(tx.Amount) || !strings.EqualFold(gwPayload.Currency, tx.Currency) {
				return fmt.Errorf("reference %s: expected %s %s, got %s %s: %w",
					tx.ReferenceNumber, tx.Amount, tx.Currency, gwPayload.Amount, gwPayload.Currency, ErrAmountMismatch)
			}
		}

		tx.Status = newStatus
		tx.PaymentMethod = gwPayload.PaymentMethod
		if tx.GatewayMetadata == nil {
			tx.GatewayMetadata = make(map[string]string)
		}
		tx.GatewayMetadata["gateway_transaction_id"] = gwPayload.GatewayTransactionID
		tx.GatewayMetadata["webhook_timestamp"] = gwPayload.Timestamp

		if err := repo.Update(ctx, tx); err != nil {
			return fmt.Errorf("failed to update transaction status: %w", err)
		}

		advance = true
		advanceTask = tx.TaskID
		finalStatus = tx.Status
		refNum = tx.ReferenceNumber
		amount = tx.Amount.String()
		currency = tx.Currency
		return nil
	})
	if err != nil {
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionWebhook, GatewayID: gatewayID,
			Reference: gwPayload.ReferenceNumber, Failure: true, Error: err.Error(),
		})
		return nil, err
	}

	// webhookResp is the gateway-specific acknowledgement parsed alongside the
	// payload above; relay it on every success path.

	// Already terminal / nothing claimed — don't advance again.
	if !advance {
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionWebhook, GatewayID: gatewayID,
			Reference: gwPayload.ReferenceNumber, Status: string(finalStatus),
		})
		return webhookResp, nil
	}

	slog.InfoContext(ctx, "processed webhook successfully", "reference", gwPayload.ReferenceNumber, "status", finalStatus)

	// Advance the suspended workflow step OUTSIDE the transaction so the row lock
	// is never held across the task-engine call. Only SUCCESS and FAILED map to a
	// task signal; any other status leaves the task untouched so a non-terminal or
	// unrecognized gateway status can't be misread as paid.
	if s.taskCompleter == nil {
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionWebhook, GatewayID: gatewayID,
			Reference: gwPayload.ReferenceNumber, Status: string(finalStatus),
		})
		return webhookResp, nil
	}

	var statusStr string
	switch finalStatus {
	case PaymentStatusSuccess:
		statusStr = "success"
	case PaymentStatusFailed:
		statusStr = "fail"
	}
	if statusStr == "" {
		slog.WarnContext(ctx, "payment: non-terminal webhook status, not advancing task",
			"reference", gwPayload.ReferenceNumber, "task_id", advanceTask, "status", finalStatus)
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionWebhook, GatewayID: gatewayID,
			Reference: gwPayload.ReferenceNumber, Status: string(finalStatus),
		})
		return webhookResp, nil
	}

	// Carry the settled transaction's facts (not just the status) so completion-
	// state UIs can render the reference and amount. These come from the
	// authoritative DB transaction (captured under lock), not gwPayload, which
	// may omit amount/currency on a FAILED notification.
	if err := s.taskCompleter.CompleteTaskStep(ctx, advanceTask, map[string]any{
		"payment_status":   statusStr,
		"reference_number": refNum,
		"amount":           amount,
		"currency":         currency,
	}); err != nil {
		// The transaction is already persisted; log and let the gateway retry
		// drive a re-attempt rather than masking the failure as success.
		slog.ErrorContext(ctx, "payment: failed to advance task step", "task_id", advanceTask, "error", err)
		s.auditPayment(ctx, AuditEvent{
			Action: AuditActionWebhook, GatewayID: gatewayID,
			Reference: gwPayload.ReferenceNumber, Status: string(finalStatus),
			Failure: true, Error: err.Error(),
		})
		return nil, fmt.Errorf("failed to advance task step for %s: %w", advanceTask, err)
	}

	s.auditPayment(ctx, AuditEvent{
		Action: AuditActionWebhook, GatewayID: gatewayID,
		Reference: gwPayload.ReferenceNumber, Status: string(finalStatus),
	})

	return webhookResp, nil
}
