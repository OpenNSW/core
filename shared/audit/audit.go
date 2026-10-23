// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package audit defines a domain-agnostic auditor callback and event shape.
// Domain-specific payloads live in the emitting package as Details implementations.
package audit

import (
	"context"
	"time"
)

// Action is a CRUD operation, matching Argus audit-log actions.
type Action string

const (
	ActionCreate Action = "CREATE"
	ActionRead   Action = "READ"
	ActionUpdate Action = "UPDATE"
	ActionDelete Action = "DELETE"
)

// Status is the outcome of an audited operation, matching Argus audit-log status.
type Status string

const (
	StatusSuccess Status = "SUCCESS"
	StatusFailure Status = "FAILURE"
)

// Details is a domain-owned payload attached to an Event. Each service
// (payment, storage, …) defines its own type that implements Metadata.
// Nil Details means the event has nothing extra to attach.
type Details interface {
	Metadata() map[string]any
}

// Event is a domain-agnostic audit record. Fields line up with
// github.com/LSFLK/argus/pkg/audit.AuditLogRequest so a later bridge is a
// straight field copy (Details.Metadata() becomes Metadata).
type Event struct {
	TraceID    string
	Timestamp  time.Time
	EventType  string
	Action     Action
	Status     Status
	ActorType  string
	ActorID    string
	TargetType string
	TargetID   string

	Details Details
}

// Auditor is an optional callback that services use to emit audit events.
// Implementations must be safe to call from any goroutine.
type Auditor interface {
	Audit(ctx context.Context, e Event)
}
