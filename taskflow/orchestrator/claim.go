// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/OpenNSW/core/taskflow/store"
)

// A claim records who is working a task, so two people do not act on it at once. The holder is an
// opaque value chosen by the host (for example an internal user ID). taskflow only stores claims:
// it never decides who may claim a task or what a claim is required for. That is the host's
// policy, enforced in its own handlers before it calls CompleteTaskStep.
//
// Not to be confused with store.TaskStore.ClaimStep, which makes a step the task's active step, or
// with renderer.Facts.Claims, which are authorization facts for rendering.

// ErrHolderRequired is returned by ClaimTask and ReleaseTask when the holder is empty. A caller
// serving HTTP should answer it with 400; it usually means the caller has no identity to claim as.
var ErrHolderRequired = errors.New("claim holder is required")

// ErrClaimHeld means another holder has the task's claim. ClaimTask returns it as a
// *ClaimHeldError, which names the holder. A caller serving HTTP should answer it with 409.
var ErrClaimHeld = errors.New("task is claimed by another holder")

// ErrNotClaimHolder is returned by ReleaseTask when the task is claimed by someone else. A caller
// serving HTTP should answer it with 403.
var ErrNotClaimHolder = errors.New("task is not claimed by the caller")

// ErrTaskCompleted is returned by ClaimTask when the task has completed: there is nothing left to
// work on. A caller serving HTTP should answer it with 409.
var ErrTaskCompleted = errors.New("task is completed")

// ClaimHeldError is the error ClaimTask returns when another holder has the claim. It matches
// ErrClaimHeld with errors.Is.
type ClaimHeldError struct {
	// Holder is who has the claim.
	Holder string
	// ClaimedAt is when Holder claimed the task, if known.
	ClaimedAt *time.Time
}

func (e *ClaimHeldError) Error() string {
	return fmt.Sprintf("%s: %s", ErrClaimHeld, e.Holder)
}

func (e *ClaimHeldError) Is(target error) bool { return target == ErrClaimHeld }

// claimAttempts bounds the retries of a claim or release whose guard failed because the claim
// changed between the write and the read that explains it (for example, the holder released in
// between). Each retry needs another concurrent change to fail again, so a few are plenty.
const claimAttempts = 3

// ClaimTask claims the task for holder. It succeeds when the task is unclaimed or already claimed
// by holder; a repeat claim keeps the original claim time. The task's steps and version are not
// changed, so an open step stays current.
//
// Errors: ErrHolderRequired, ErrTaskNotFound, ErrTaskCompleted (the task has completed, even if
// holder had claimed it), or a *ClaimHeldError (another holder has the claim).
func (tm *TaskManager) ClaimTask(ctx context.Context, taskID, holder string) error {
	if holder == "" {
		return ErrHolderRequired
	}
	for range claimAttempts {
		n, err := tm.db.ClaimTask(ctx, taskID, holder, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("claiming task %s: %w", taskID, err)
		}
		if n > 0 {
			return nil
		}

		// The guard did not match: read the row to say why.
		record, ok := tm.db.GetTask(ctx, taskID)
		switch {
		case !ok:
			return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
		case record.State == store.StateCompleted:
			return fmt.Errorf("%w: %s", ErrTaskCompleted, taskID)
		case record.ClaimedBy != "" && record.ClaimedBy != holder:
			return &ClaimHeldError{Holder: record.ClaimedBy, ClaimedAt: record.ClaimedAt}
		}
		// The row is claimable now, so the claim changed after the write: try again.
	}
	return fmt.Errorf("claiming task %s: the claim kept changing, try again", taskID)
}

// ReleaseTask clears holder's claim on the task. Releasing a task that is not claimed succeeds, so
// a repeated release is harmless. A completed task's claim can still be released.
//
// Errors: ErrHolderRequired, ErrTaskNotFound, or ErrNotClaimHolder (someone else has the claim).
func (tm *TaskManager) ReleaseTask(ctx context.Context, taskID, holder string) error {
	if holder == "" {
		return ErrHolderRequired
	}
	for range claimAttempts {
		n, err := tm.db.ReleaseTask(ctx, taskID, holder)
		if err != nil {
			return fmt.Errorf("releasing task %s: %w", taskID, err)
		}
		if n > 0 {
			return nil
		}

		record, ok := tm.db.GetTask(ctx, taskID)
		switch {
		case !ok:
			return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
		case record.ClaimedBy == "":
			return nil
		case record.ClaimedBy != holder:
			return fmt.Errorf("%w: %s", ErrNotClaimHolder, taskID)
		}
		// holder has the claim now, so it changed after the write: try again.
	}
	return fmt.Errorf("releasing task %s: the claim kept changing, try again", taskID)
}
