// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import "context"

// Auditor is an optional callback that a PaymentService implementation uses to
// emit audit events after payment operations complete. Implementations must be
// safe to call from any goroutine.
type Auditor interface {
	AuditPayment(ctx context.Context, e AuditEvent)
}

// AuditAction describes what operation was performed.
type AuditAction string

const (
	AuditActionWebhook  AuditAction = "UPDATE"
	AuditActionValidate AuditAction = "READ"
)

// AuditEvent carries the domain-rich details of a payment operation.
type AuditEvent struct {
	Action    AuditAction
	GatewayID string
	Reference string
	Status    string // domain payment status if available
	Failure   bool
	Error     string
}
