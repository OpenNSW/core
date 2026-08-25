// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import "context"

// Auditor is an optional callback that a Service uses to emit audit events
// after storage operations complete. Implementations must be safe to call
// from any goroutine.
type Auditor interface {
	AuditStorage(ctx context.Context, e AuditEvent)
}

// AuditAction describes what operation was performed.
type AuditAction string

const (
	AuditActionGetUploadURL AuditAction = "PRESIGN_UPLOAD"
	AuditActionDownload     AuditAction = "READ"
	AuditActionDelete       AuditAction = "DELETE"
)

// AuditEvent carries the domain-rich details of a storage operation.
type AuditEvent struct {
	Action   AuditAction
	Key      string
	Filename string
	MimeType string
	Size     int64
	Failure  bool
	Error    string
}
