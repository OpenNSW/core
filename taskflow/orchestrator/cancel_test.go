// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/taskflow/store"
)

const cancelTaskID = "cancel-task"

func cancelFixture(t *testing.T, state string) (*TaskManager, *safeMockTaskStore, *mockTemporalManager) {
	t.Helper()
	db := newSafeMockTaskStore()
	if err := db.InitTask(context.Background(), store.TaskRecord{
		TaskID: cancelTaskID, TaskType: "TEST", State: state, TaskWorkflowID: "task-wf-" + cancelTaskID, Data: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	wm := &mockTemporalManager{}
	return newTestTaskManager(db, newTestRegistry(), wm, noopCallback), db, wm
}

// Cancelling a task terminates its workflow, with the reason, and marks it cancelled.
func TestCancelTask_TerminatesTheWorkflowAndMarksTheTask(t *testing.T) {
	tm, db, wm := cancelFixture(t, "PENDING_USER")

	if err := tm.CancelTask(context.Background(), cancelTaskID, "case called off"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	if got := wm.terminated["task-wf-"+cancelTaskID]; got != "case called off" {
		t.Errorf("terminated with reason %q, want %q", got, "case called off")
	}
	row, _ := db.GetTask(context.Background(), cancelTaskID)
	if row.State != store.StateCancelled {
		t.Errorf("State = %q, want %q", row.State, store.StateCancelled)
	}
}

// A task that has already ended keeps the way it ended, and nothing is terminated.
func TestCancelTask_LeavesAnEndedTask(t *testing.T) {
	for _, state := range []string{store.StateCompleted, store.StateCancelled} {
		tm, db, wm := cancelFixture(t, state)

		if err := tm.CancelTask(context.Background(), cancelTaskID, "again"); err != nil {
			t.Fatalf("%s: CancelTask: %v", state, err)
		}
		if len(wm.terminated) != 0 {
			t.Errorf("%s: terminated %v, want nothing", state, wm.terminated)
		}
		if row, _ := db.GetTask(context.Background(), cancelTaskID); row.State != state {
			t.Errorf("%s: State = %q, want it unchanged", state, row.State)
		}
	}
}

// A workflow that cannot be terminated leaves the task as it was, for the caller to retry.
func TestCancelTask_TerminationFailure(t *testing.T) {
	tm, db, wm := cancelFixture(t, "PENDING_USER")
	wm.terminateErr = errors.New("temporal unavailable")

	if err := tm.CancelTask(context.Background(), cancelTaskID, "x"); err == nil {
		t.Fatal("CancelTask: want an error")
	}
	if row, _ := db.GetTask(context.Background(), cancelTaskID); row.State != "PENDING_USER" {
		t.Errorf("State = %q, want it unchanged", row.State)
	}
}

func TestCancelTask_UnknownTask(t *testing.T) {
	tm, _, _ := cancelFixture(t, "PENDING_USER")

	if err := tm.CancelTask(context.Background(), "nope", "x"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("CancelTask = %v, want ErrTaskNotFound", err)
	}
}
