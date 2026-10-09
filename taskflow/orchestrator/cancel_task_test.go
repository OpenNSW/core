// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/OpenNSW/core/taskflow/store"
	engine "github.com/OpenNSW/core/workflow"
)

const (
	cancelTaskID     = "task-1"
	cancelWorkflowID = "task-wf-task-1"
	cancelStepID     = "0a0a0a0a-0000-4000-8000-00000000000a"
)

// cancelFixture returns a TaskManager over a store holding one task waiting on its user at seq 3,
// and the list the mock appends each terminated workflow ID to.
func cancelFixture(t *testing.T, terminateErr error) (*TaskManager, *safeMockTaskStore, *[]string) {
	t.Helper()
	db := newSafeMockTaskStore()
	ctx := context.Background()
	if err := db.InitTask(ctx, store.TaskRecord{TaskID: cancelTaskID, TaskWorkflowID: cancelWorkflowID, State: "STARTING"}); err != nil {
		t.Fatal(err)
	}
	_, _ = db.ClaimStep(ctx, cancelTaskID, store.StepClaim{StepID: cancelStepID, Seq: 3, State: store.StateStartingStep})
	_, _ = db.WriteRenderState(ctx, cancelTaskID, cancelStepID, 3, "PENDING_USER", nil)

	var terminated []string
	wf := &mockTemporalManager{terminateFunc: func(_ context.Context, workflowID, _, _ string) error {
		terminated = append(terminated, workflowID)
		return terminateErr
	}}
	return newTestTaskManager(db, newTestRegistry(), wf, noopCallback), db, &terminated
}

func TestCancelTask_ClosesTheTaskAndStopsItsWorkflow(t *testing.T) {
	tm, db, terminated := cancelFixture(t, nil)

	if err := tm.CancelTask(context.Background(), cancelTaskID, "warranted offline"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	got, _ := db.GetTask(context.Background(), cancelTaskID)
	if got.State != store.StateCancelled || got.Seq != 4 {
		t.Fatalf("row = %s at seq %d, want %s at seq 4", got.State, got.Seq, store.StateCancelled)
	}
	if len(*terminated) != 1 || (*terminated)[0] != cancelWorkflowID {
		t.Fatalf("terminated %v, want [%s]", *terminated, cancelWorkflowID)
	}
}

func TestCancelTask_CompletedOrMissingTaskIsNotOpen(t *testing.T) {
	tm, db, terminated := cancelFixture(t, nil)
	_, _ = db.CompleteTask(context.Background(), cancelTaskID, 9)

	for _, id := range []string{cancelTaskID, "nope"} {
		err := tm.CancelTask(context.Background(), id, "reason")
		if !errors.Is(err, ErrTaskNotOpen) {
			t.Fatalf("CancelTask(%s) = %v, want ErrTaskNotOpen", id, err)
		}
	}
	if len(*terminated) != 0 {
		t.Fatalf("terminated %v, want nothing", *terminated)
	}
}

// A task workflow that has already closed was terminated by an earlier call, or finished; either
// way the task is closed.
func TestCancelTask_WorkflowAlreadyClosed(t *testing.T) {
	tm, _, _ := cancelFixture(t, fmt.Errorf("%w: already completed", engine.ErrWorkflowNotFound))

	if err := tm.CancelTask(context.Background(), cancelTaskID, "reason"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
}

// A call that failed after marking the row can be made again, and terminates the workflow then.
func TestCancelTask_CanBeRepeatedAfterAFailure(t *testing.T) {
	tm, db, terminated := cancelFixture(t, errors.New("temporal unavailable"))

	if err := tm.CancelTask(context.Background(), cancelTaskID, "reason"); err == nil {
		t.Fatal("CancelTask succeeded, want the terminate error")
	}
	got, _ := db.GetTask(context.Background(), cancelTaskID)
	if got.State != store.StateCancelled {
		t.Fatalf("row = %s, want %s", got.State, store.StateCancelled)
	}

	tm.taskWorkflowManager.(*mockTemporalManager).terminateFunc = func(_ context.Context, workflowID, _, _ string) error {
		*terminated = append(*terminated, workflowID)
		return nil
	}
	if err := tm.CancelTask(context.Background(), cancelTaskID, "reason"); err != nil {
		t.Fatalf("second CancelTask: %v", err)
	}
	if len(*terminated) != 2 {
		t.Fatalf("terminated %v, want two attempts", *terminated)
	}
}

// A submission for a cancelled task is stale before it reaches the workflow.
func TestCompleteTaskStep_CancelledTaskIsStale(t *testing.T) {
	tm, _, _ := cancelFixture(t, nil)
	if err := tm.CancelTask(context.Background(), cancelTaskID, "reason"); err != nil {
		t.Fatal(err)
	}
	tm.taskWorkflowManager.(*mockTemporalManager).taskDoneFunc = func(context.Context, string, string, string, map[string]any) error {
		t.Fatal("the submission reached the workflow")
		return nil
	}

	err := tm.CompleteTaskStep(context.Background(), cancelTaskID, cancelStepID, map[string]any{"a": 1})
	if !errors.Is(err, ErrStaleStep) {
		t.Fatalf("CompleteTaskStep = %v, want ErrStaleStep", err)
	}
}
