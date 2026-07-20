// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package asycuda implements webhook handlers and DTOs for receiving
// asynchronous callbacks from the ASYCUDA customs system (CIG).
//
// All struct definitions and JSON field mappings are based on the ASYCUDA API
// Specification v1.2. Note: the actual payloads use "event" (not "eventType")
// and "processAt" (not "processedAt") — a known drift from the spec prose.
package asycuda

import (
	"errors"
	"time"
)

// DispatchNoteStatus represents the lifecycle state of a Cargo Dispatch Note.
type DispatchNoteStatus string

const (
	DispatchNoteStatusSubmitted    DispatchNoteStatus = "SUBMITTED"
	DispatchNoteStatusIntegrated   DispatchNoteStatus = "INTEGRATED"
	DispatchNoteStatusFailed       DispatchNoteStatus = "FAILED"
	DispatchNoteStatusAcknowledged DispatchNoteStatus = "ACKNOWLEDGED"
)

// --------------------------------------------------------
// Shared value objects
// --------------------------------------------------------

// DocumentReference represents the ASYCUDA CDN reference (cdnRef), a composite
// key that uniquely identifies a Cargo Dispatch Note within the customs system.
type DocumentReference struct {
	Year   string `json:"year"`
	Office string `json:"office"`
	Serial string `json:"serial"`
	Number int    `json:"number"`
}

// IsZero reports whether the reference is empty / unset.
func (r DocumentReference) IsZero() bool {
	return r.Year == "" && r.Office == "" && r.Serial == "" && r.Number == 0
}

// IsComplete reports whether all fields of the reference are populated with valid values.
func (r DocumentReference) IsComplete() bool {
	return r.Year != "" && r.Office != "" && r.Serial != "" && r.Number > 0
}

// --------------------------------------------------------
// §7.2 — CDN Integration Result callback DTO
// --------------------------------------------------------

// integrationResultPayload is the nested "payload" object inside the §7.2
// callback. It carries the assigned CDN reference on success.
type integrationResultPayload struct {
	CDNRef DocumentReference `json:"cdnRef"`
}

// CDNIntegrationResultRequest is the inbound DTO for the ASYCUDA §7.2 callback
// pushed when CDN integration succeeds or fails.
//
// JSON tags reflect the *actual* field names returned by ASYCUDA (not the
// documentation names): "event" instead of "eventType", "processAt" instead of
// "processedAt".
type CDNIntegrationResultRequest struct {
	// EdgID is the correlation UUID saved when the CDN was originally submitted.
	EdgID string `json:"edgId"`

	// Integrated is true when ASYCUDA successfully integrated the CDN.
	Integrated bool `json:"integrated"`

	// Event is the callback event type (actual field name, not "eventType").
	Event string `json:"event"`

	// ProcessAt is the timestamp when ASYCUDA processed the CDN (actual field
	// name, not "processedAt").
	ProcessAt time.Time `json:"processAt"`

	// Payload carries the CDN reference assigned by customs on success.
	Payload integrationResultPayload `json:"payload"`

	// Errors contains error details when integration fails. Keys and structure
	// are defined by ASYCUDA and vary by failure mode.
	Errors map[string]any `json:"errors,omitempty"`
}

// validate checks that a CDNIntegrationResultRequest is well-formed.
func (r CDNIntegrationResultRequest) validate() error {
	if r.EdgID == "" {
		return errors.New("edgId is required")
	}
	if r.Event == "" {
		return errors.New("event is required")
	}
	if r.ProcessAt.IsZero() {
		return errors.New("processAt is required")
	}
	// When integration succeeds the payload MUST carry a complete cdnRef.
	if r.Integrated && !r.Payload.CDNRef.IsComplete() {
		return errors.New("payload.cdnRef must be fully populated when integrated is true")
	}
	return nil
}

// --------------------------------------------------------
// §7.3 — CDN Acknowledgment callback DTO
// --------------------------------------------------------

// acknowledgmentPayload is the nested "payload" object inside the §7.3 callback.
type acknowledgmentPayload struct {
	CDNRef DocumentReference `json:"cdnRef"`
}

// CDNAcknowledgmentRequest is the inbound DTO for the ASYCUDA §7.3 callback
// pushed as a notification after the CDN has been acknowledged.
//
// JSON tags reflect the *actual* field names (see CDNIntegrationResultRequest).
type CDNAcknowledgmentRequest struct {
	// Event is the callback event type.
	Event string `json:"event"`

	// ProcessAt is the timestamp when ASYCUDA processed the acknowledgment.
	ProcessAt time.Time `json:"processAt"`

	// Payload carries the CDN reference for correlation.
	Payload acknowledgmentPayload `json:"payload"`
}

// validate checks that a CDNAcknowledgmentRequest is well-formed.
func (r CDNAcknowledgmentRequest) validate() error {
	if r.Event == "" {
		return errors.New("event is required")
	}
	if r.ProcessAt.IsZero() {
		return errors.New("processAt is required")
	}
	if !r.Payload.CDNRef.IsComplete() {
		return errors.New("payload.cdnRef must be fully populated")
	}
	return nil
}

// --------------------------------------------------------
// Domain entity
// --------------------------------------------------------

// DispatchNote is the domain entity representing a Cargo Dispatch Note. It is
// created when the CDN is submitted to ASYCUDA and updated as callbacks arrive.
type DispatchNote struct {
	ID        string             `json:"id" gorm:"type:text;not null;primaryKey"`
	EdgID     string             `json:"edg_id" gorm:"type:text;not null;uniqueIndex"` // Correlation UUID from submission
	Status    DispatchNoteStatus `json:"status" gorm:"type:text;not null;index"`
	CDNYear   string             `json:"cdn_year" gorm:"index:idx_cdn_ref"`
	CDNOffice string             `json:"cdn_office" gorm:"index:idx_cdn_ref"`
	CDNSerial string             `json:"cdn_serial" gorm:"index:idx_cdn_ref"`
	CDNNumber int                `json:"cdn_number" gorm:"index:idx_cdn_ref"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}
