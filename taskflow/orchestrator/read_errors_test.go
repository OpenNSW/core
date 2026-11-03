// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/taskflow/store"
	engine "github.com/OpenNSW/core/workflow"
)

// failingReadStore is a store whose task reads fail with err, as when the database is down.
type failingReadStore struct {
	*safeMockTaskStore
	err error
}

func (s failingReadStore) GetTask(context.Context, string) (store.TaskRecord, error) {
	return store.TaskRecord{}, s.err
}

func (s failingReadStore) GetTaskByWorkflowID(context.Context, string) (store.TaskRecord, error) {
	return store.TaskRecord{}, s.err
}

var errDBDown = errors.New("db down")

// A failed read of the task fails the completion activity, so Temporal retries it. Treating it as
// "not our workflow" would drop the completion and leave the parent waiting forever.
func TestHandleTaskCompletion_FailsWhenTheTaskCannotBeRead(t *testing.T) {
	woken := false
	_, db := completionFixture(t, nil)
	tm := newTestTaskManager(failingReadStore{db, errDBDown}, newTestRegistry(), &mockTemporalManager{},
		func(string, string, map[string]any) error { woken = true; return nil })

	if err := tm.HandleTaskCompletion(context.Background(), completion(6)); !errors.Is(err, errDBDown) {
		t.Fatalf("HandleTaskCompletion err = %v, want %v", err, errDBDown)
	}
	if woken {
		t.Error("parent was woken although the task could not be read")
	}
	if row := wakeRow(t, db); row.State == store.StateCompleted {
		t.Error("row was marked completed although the task could not be read")
	}
}

// A completion for a workflow with no task is still ignored.
func TestHandleTaskCompletion_IgnoresAnUnknownWorkflow(t *testing.T) {
	tm, _ := completionFixture(t, func(string, string, map[string]any) error {
		t.Error("parent woken for an unknown workflow")
		return nil
	})
	if err := tm.HandleTaskCompletion(context.Background(), engine.WorkflowCompletion{WorkflowID: "unknown-wf", Seq: 1}); err != nil {
		t.Errorf("HandleTaskCompletion(unknown) = %v, want nil", err)
	}
}

// A failed read of the task fails the hold, so it is retried rather than dropped.
func TestHoldTask_FailsWhenTheTaskCannotBeRead(t *testing.T) {
	_, db, _ := cancelFixture(t, nil)
	tm := newTestTaskManager(failingReadStore{db, errDBDown}, newTestRegistry(), &mockTemporalManager{}, noopCallback)

	if err := tm.HoldTask(context.Background(), engine.AdminParkPayload{WorkflowID: cancelWorkflowID, Seq: 3}); !errors.Is(err, errDBDown) {
		t.Fatalf("HoldTask err = %v, want %v", err, errDBDown)
	}
	if got, _ := db.GetTask(context.Background(), cancelTaskID); got.State == store.StateOnHold {
		t.Error("task was held although it could not be read")
	}
}

// The methods a host serves over HTTP return a failed read as that error, never as
// ErrTaskNotFound, so the host answers 5xx rather than 404.
func TestTaskReads_ReturnAFailedReadAsNotNotFound(t *testing.T) {
	_, db, _ := cancelFixture(t, nil)
	tm := newTestTaskManager(failingReadStore{db, errDBDown}, newTestRegistry(), &mockTemporalManager{}, noopCallback)
	ctx := context.Background()

	_, renderErr := tm.GetTaskRenderInfo(ctx, cancelTaskID)
	for name, err := range map[string]error{
		"GetTaskRenderInfo": renderErr,
		"CompleteTaskStep":  tm.CompleteTaskStep(ctx, cancelTaskID, cancelStepID, map[string]any{}),
		"SaveDraft":         tm.SaveDraft(ctx, cancelTaskID, cancelStepID, map[string]any{}),
		"ReleaseTask":       tm.ReleaseTask(ctx, cancelTaskID, "not-the-holder"), // matches no row, so it reads the task
		"CancelTask":        tm.CancelTask(ctx, cancelTaskID, "reason"),
	} {
		if !errors.Is(err, errDBDown) || errors.Is(err, ErrTaskNotFound) || errors.Is(err, ErrTaskNotOpen) {
			t.Errorf("%s err = %v, want %v and not ErrTaskNotFound or ErrTaskNotOpen", name, err, errDBDown)
		}
	}
}
