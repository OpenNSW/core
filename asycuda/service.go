// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package asycuda

import (
	"context"
	"fmt"
	"log/slog"
)

// CDNWebhookService defines the use-case layer for processing asynchronous
// ASYCUDA callbacks related to Cargo Dispatch Notes.
type CDNWebhookService interface {
	// ProcessIntegrationResult handles the §7.2 callback. It correlates the
	// callback to the original submission using the edgId, then updates the
	// dispatch note status to INTEGRATED or FAILED.
	ProcessIntegrationResult(ctx context.Context, req CDNIntegrationResultRequest) error

	// ProcessAcknowledgment handles the §7.3 callback. It correlates using the
	// cdnRef composite key and updates the dispatch note status to ACKNOWLEDGED.
	ProcessAcknowledgment(ctx context.Context, req CDNAcknowledgmentRequest) error
}

type cdnWebhookService struct {
	repo DispatchNoteRepository
}

// NewCDNWebhookService creates a new CDNWebhookService backed by the given
// repository.
func NewCDNWebhookService(repo DispatchNoteRepository) CDNWebhookService {
	return &cdnWebhookService{repo: repo}
}

// ProcessIntegrationResult looks up the dispatch note by edgId (saved during
// the initial CDN submission) and transitions its status based on the
// integration outcome.
//
// On success (integrated == true):
//   - Status → INTEGRATED
//   - Stores the cdnRef assigned by ASYCUDA
//
// On failure (integrated == false):
//   - Status → FAILED
//   - The errors map is logged for diagnostics
func (s *cdnWebhookService) ProcessIntegrationResult(ctx context.Context, req CDNIntegrationResultRequest) error {
	slog.InfoContext(ctx, "processing CDN integration result",
		"edg_id", req.EdgID,
		"integrated", req.Integrated,
		"event", req.Event,
	)

	note, err := s.repo.GetByEdgID(ctx, req.EdgID)
	if err != nil {
		return fmt.Errorf("failed to retrieve dispatch note by edgId %s: %w", req.EdgID, err)
	}
	if note == nil {
		slog.WarnContext(ctx, "no dispatch note found for edgId", "edg_id", req.EdgID)
		return fmt.Errorf("edgId %s: %w", req.EdgID, ErrDispatchNoteNotFound)
	}

	// If the note is already acknowledged, do not overwrite its status.
	if note.Status == DispatchNoteStatusAcknowledged {
		slog.InfoContext(ctx, "dispatch note already acknowledged, ignoring integration result", "edg_id", req.EdgID)
		return nil
	}

	if req.Integrated {
		// If already integrated, treat as idempotent success.
		if note.Status == DispatchNoteStatusIntegrated {
			slog.InfoContext(ctx, "dispatch note already integrated, ignoring duplicate callback", "edg_id", req.EdgID)
			return nil
		}

		note.Status = DispatchNoteStatusIntegrated
		note.CDNYear = req.Payload.CDNRef.Year
		note.CDNOffice = req.Payload.CDNRef.Office
		note.CDNSerial = req.Payload.CDNRef.Serial
		note.CDNNumber = req.Payload.CDNRef.Number

		if err := s.repo.Update(ctx, note); err != nil {
			return fmt.Errorf("failed to update dispatch note to INTEGRATED: %w", err)
		}

		slog.InfoContext(ctx, "dispatch note integrated successfully",
			"edg_id", req.EdgID,
			"cdn_ref", req.Payload.CDNRef,
		)
	} else {
		// If already failed, treat as idempotent success.
		if note.Status == DispatchNoteStatusFailed {
			slog.InfoContext(ctx, "dispatch note already failed, ignoring duplicate callback", "edg_id", req.EdgID)
			return nil
		}
		// Do not transition from integrated to failed on late/duplicate failure callbacks.
		if note.Status == DispatchNoteStatusIntegrated {
			slog.WarnContext(ctx, "dispatch note already integrated, ignoring late failure callback", "edg_id", req.EdgID)
			return nil
		}

		note.Status = DispatchNoteStatusFailed
		if err := s.repo.Update(ctx, note); err != nil {
			return fmt.Errorf("failed to update dispatch note to FAILED: %w", err)
		}

		slog.WarnContext(ctx, "dispatch note integration failed",
			"edg_id", req.EdgID,
			"errors", req.Errors,
		)
	}

	return nil
}

// ProcessAcknowledgment correlates the §7.3 callback using the composite
// cdnRef and transitions the dispatch note status to ACKNOWLEDGED.
func (s *cdnWebhookService) ProcessAcknowledgment(ctx context.Context, req CDNAcknowledgmentRequest) error {
	ref := req.Payload.CDNRef

	slog.InfoContext(ctx, "processing CDN acknowledgment",
		"event", req.Event,
		"cdn_ref", ref,
	)

	note, err := s.repo.GetByCDNRef(ctx, ref)
	if err != nil {
		return fmt.Errorf("failed to retrieve dispatch note by cdnRef: %w", err)
	}
	if note == nil {
		slog.WarnContext(ctx, "no dispatch note found for cdnRef",
			"year", ref.Year,
			"office", ref.Office,
			"serial", ref.Serial,
			"number", ref.Number,
		)
		return fmt.Errorf("cdnRef %v: %w", ref, ErrDispatchNoteNotFound)
	}

	if note.Status == DispatchNoteStatusAcknowledged {
		slog.InfoContext(ctx, "dispatch note already acknowledged, ignoring duplicate callback", "cdn_ref", ref)
		return nil
	}

	note.Status = DispatchNoteStatusAcknowledged
	if err := s.repo.Update(ctx, note); err != nil {
		return fmt.Errorf("failed to update dispatch note to ACKNOWLEDGED: %w", err)
	}

	slog.InfoContext(ctx, "dispatch note acknowledged",
		"id", note.ID,
		"cdn_ref", ref,
	)

	return nil
}
