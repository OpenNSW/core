// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import "github.com/OpenNSW/core/shared/audit"

var _ audit.Details = AuditDetails{}

// AuditDetails is the storage-owned payload on an audit.Event.
type AuditDetails struct {
	Key      string
	Filename string
	MimeType string
	Size     int64
	Error    string
}

func (d AuditDetails) Metadata() map[string]any {
	m := make(map[string]any, 5)
	if d.Key != "" {
		m["key"] = d.Key
	}
	if d.Filename != "" {
		m["filename"] = d.Filename
	}
	if d.MimeType != "" {
		m["mime_type"] = d.MimeType
	}
	if d.Size != 0 {
		m["size"] = d.Size
	}
	if d.Error != "" {
		m["error"] = d.Error
	}
	return m
}
