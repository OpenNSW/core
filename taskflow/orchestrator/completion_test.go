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

// completionFixture is a task whose last step (seq 6) has been submitted, waiting for its workflow
// to end. onCompleted is the parent wake-up.
func completionFixture(t *testing.T, onCompleted TaskCompletedCallback) (*TaskManager, *safeMockTaskStore) {
	t.Helper()
	db := newSafeMockTaskStore()
	db.InitTask(context.Background(), store.TaskRecord{
		TaskID: wakeTaskID, TaskWorkflowID: "task-wf", State: "STARTING", Data: map[string]any{},
		ParentWorkflowID: "parent-wf", ParentStepID: "parent-step",
	})
	if _, err := db.ClaimStep(context.Background(), wakeTaskID, store.StepClaim{StepID: stepB, Seq: 6, State: store.StateStartingStep}); err != nil {
		t.Fatal(err)
	}
	return newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{}, onCompleted), db
}

func completion(seq int64) engine.WorkflowCompletion {
	return engine.WorkflowCompletion{WorkflowID: "task-wf", Seq: seq, FinalVariables: map[string]any{"out": "v"}}
}

// The completion is written before the parent is woken, so a crash after the write and before the
// wake-up leaves the parent to be woken by the retry.
func TestHandleTaskCompletion_MarksCompletedThenWakesTheParent(t *testing.T) {
	var db *safeMockTaskStore
	var stateWhenParentWoken string
	var gotParent [2]string
	var gotVars map[string]any
	tm, d := completionFixture(t, func(wf, step string, vars map[string]any) error {
		stateWhenParentWoken = mustGetState(t, db)
		gotParent, gotVars = [2]string{wf, step}, vars
		return nil
	})
	db = d

	if err := tm.HandleTaskCompletion(context.Background(), completion(6)); err != nil {
		t.Fatal(err)
	}

	if stateWhenParentWoken != store.StateCompleted {
		t.Errorf("row was %q when the parent was woken, want COMPLETED first", stateWhenParentWoken)
	}
	if gotParent != [2]string{"parent-wf", "parent-step"} || gotVars["out"] != "v" {
		t.Errorf("parent woken with %v %v", gotParent, gotVars)
	}
	if row := wakeRow(t, db); row.State != store.StateCompleted || row.Seq != 6 {
		t.Errorf("row = (%q, %d), want (COMPLETED, 6)", row.State, row.Seq)
	}
}

func mustGetState(t *testing.T, db *safeMockTaskStore) string {
	t.Helper()
	return wakeRow(t, db).State
}

// A crash between the two halves: the first attempt writes COMPLETED and fails to wake the parent.
// Temporal retries. A completed row must not short-circuit the retry, or the parent is never woken.
func TestHandleTaskCompletion_RetryAfterCrashBetweenTheHalvesFinishes(t *testing.T) {
	attempts := 0
	tm, db := completionFixture(t, func(_, _ string, _ map[string]any) error {
		attempts++
		if attempts == 1 {
			return errors.New("parent unreachable")
		}
		return nil
	})

	if err := tm.HandleTaskCompletion(context.Background(), completion(6)); err == nil {
		t.Fatal("expected the first attempt to fail so Temporal retries")
	}
	if wakeRow(t, db).State != store.StateCompleted {
		t.Fatal("the completion is written before the parent is woken, so it stands after a failed wake-up")
	}

	if err := tm.HandleTaskCompletion(context.Background(), completion(6)); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if attempts != 2 {
		t.Errorf("parent woken %d times, want the retry to wake it", attempts)
	}
	if row := wakeRow(t, db); row.State != store.StateCompleted || row.Seq != 6 {
		t.Errorf("row = (%q, %d)", row.State, row.Seq)
	}
}

// If the parent's step is already complete an earlier attempt woke it, whose success was lost.
func TestHandleTaskCompletion_ParentAlreadyResumedIsSuccess(t *testing.T) {
	tm, db := completionFixture(t, func(_, _ string, _ map[string]any) error {
		return errors.Join(engine.ErrActivationNotPending, errors.New("activity not found"))
	})

	if err := tm.HandleTaskCompletion(context.Background(), completion(6)); err != nil {
		t.Fatalf("err = %v, want success: the parent was already woken", err)
	}
	if wakeRow(t, db).State != store.StateCompleted {
		t.Error("the task is still marked completed")
	}
}

func TestHandleTaskCompletion_OtherCallbackErrorsAreReturned(t *testing.T) {
	callbackErr := errors.New("parent unreachable")
	tm, _ := completionFixture(t, func(_, _ string, _ map[string]any) error { return callbackErr })

	if err := tm.HandleTaskCompletion(context.Background(), completion(6)); !errors.Is(err, callbackErr) {
		t.Fatalf("err = %v, want the callback error so Temporal retries", err)
	}
}

// Nothing should be past the end of the workflow, but if the row is, the parent must still be woken:
// the workflow has ended and holding it back would hang the parent.
func TestHandleTaskCompletion_RowPastTheCompletionStillWakesTheParent(t *testing.T) {
	woken := false
	tm, db := completionFixture(t, func(_, _ string, _ map[string]any) error { woken = true; return nil })

	if err := tm.HandleTaskCompletion(context.Background(), completion(5)); err != nil {
		t.Fatal(err)
	}
	if !woken {
		t.Error("parent not woken")
	}
	if row := wakeRow(t, db); row.State == store.StateCompleted || row.Seq != 6 {
		t.Errorf("row = (%q, %d): a completion older than the row must not overwrite it", row.State, row.Seq)
	}
}
