// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import "github.com/OpenNSW/core/shared/audit"

var _ audit.Details = AuditDetails{}

// AuditDetails is the payment-owned payload on an audit.Event.
type AuditDetails struct {
	GatewayID string
	Reference string
	Status    string // domain PaymentStatus, not audit.Status
	Error     string
}

func (d AuditDetails) Metadata() map[string]any {
	m := make(map[string]any, 4)
	if d.GatewayID != "" {
		m["gateway_id"] = d.GatewayID
	}
	if d.Reference != "" {
		m["reference"] = d.Reference
	}
	if d.Status != "" {
		m["status"] = d.Status
	}
	if d.Error != "" {
		m["error"] = d.Error
	}
	return m
}
