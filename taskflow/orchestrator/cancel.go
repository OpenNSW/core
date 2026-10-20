// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"fmt"

	"github.com/OpenNSW/core/taskflow/store"
)

// CancelTask ends a task before its workflow finishes, for a host whose case has been called off
// by something outside it, such as a regulator withdrawing what the task was waiting on. The task's
// workflow is terminated, recording reason, and the task is marked store.StateCancelled: it takes no
// further steps or submissions, and a step write still in flight for it changes nothing.
//
// The task's completion callback does not run: the host ends whatever the task belonged to itself.
// A task that has already ended, completed or cancelled, is left as it is, so a repeated call
// succeeds. Errors: ErrTaskNotFound, or the failure to terminate the workflow or record the state.
func (tm *TaskManager) CancelTask(ctx context.Context, taskID, reason string) error {
	record, ok := tm.db.GetTask(ctx, taskID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	if record.State == store.StateCompleted || record.State == store.StateCancelled {
		return nil
	}
	// Terminated first: were the row marked first and the termination then failed, the workflow
	// would carry on against a row that refuses it. A retry after a failed mark terminates again,
	// which is harmless.
	if record.TaskWorkflowID != "" {
		if err := tm.taskWorkflowManager.TerminateWorkflow(ctx, record.TaskWorkflowID, reason); err != nil {
			return fmt.Errorf("cancelling task %s: %w", taskID, err)
		}
	}
	if _, err := tm.db.CancelTask(ctx, taskID); err != nil {
		return fmt.Errorf("cancelling task %s: %w", taskID, err)
	}
	tm.logger.InfoContext(ctx, "task cancelled", "task_id", taskID, "reason", reason)
	return nil
}
