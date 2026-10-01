// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/OpenNSW/core/taskflow/callbacktoken"
	"github.com/OpenNSW/core/taskflow/store"
	engine "github.com/OpenNSW/core/workflow"
)

const wakeTaskID = "5f0c9b1e-3d2a-4c6b-8e7f-0a1b2c3d4e5f"

// wakeUpFixture is a task whose step A (seq 4, template generic_user_input, output namespace
// "userform") is waiting for the user, the state StartTaskStep leaves it in.
func wakeUpFixture(t *testing.T, db store.TaskStore) (*TaskManager, *mockTemporalManager) {
	t.Helper()
	ctx := context.Background()
	db.InitTask(ctx, store.TaskRecord{TaskID: wakeTaskID, TaskType: "TEST", State: "STARTING", TaskWorkflowID: "task-wf", Data: map[string]any{}})
	if _, err := db.ClaimStep(ctx, wakeTaskID, store.StepClaim{
		StepID: stepA, Seq: 4, ActiveTaskTemplateID: "generic_user_input", State: store.StateStartingStep,
		Data: map[string]any{"in": "value"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.WriteRenderState(ctx, wakeTaskID, stepA, 4, "PENDING_USER", map[string]any{"in": "value"}); err != nil {
		t.Fatal(err)
	}
	wf := &mockTemporalManager{}
	return newTestTaskManager(db, newTestRegistry(), wf, noopCallback), wf
}

func wakeRow(t *testing.T, db store.TaskStore) store.TaskRecord {
	t.Helper()
	row, ok := db.GetTask(context.Background(), wakeTaskID)
	if !ok {
		t.Fatal("task row missing")
	}
	return row
}

func TestCompleteTaskStep_RequiresAStepID(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, wf := wakeUpFixture(t, db)
	called := false
	wf.taskDoneFunc = func(context.Context, string, string, string, map[string]any) error { called = true; return nil }

	err := tm.CompleteTaskStep(context.Background(), wakeTaskID, "", map[string]any{"x": 1})

	if !errors.Is(err, ErrStepIDRequired) {
		t.Fatalf("err = %v, want ErrStepIDRequired", err)
	}
	if called || wakeRow(t, db).State != "PENDING_USER" {
		t.Error("a call without a step ID must change nothing")
	}
}

func TestCompleteTaskStep_CompletesTheStepAndPersistsTheSubmission(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, wf := wakeUpFixture(t, db)
	var gotWF, gotRun, gotStep string
	var gotOut map[string]any
	wf.taskDoneFunc = func(_ context.Context, workflowID, runID, stepID string, out map[string]any) error {
		gotWF, gotRun, gotStep, gotOut = workflowID, runID, stepID, out
		return nil
	}

	payload := map[string]any{"name": "Alice", "__command": "submit"}
	if err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, payload); err != nil {
		t.Fatal(err)
	}

	if gotWF != "task-wf" || gotRun != "" || gotStep != stepA {
		t.Errorf("CompleteActivation(%q, %q, %q), want the task workflow, no run ID, and the step ID", gotWF, gotRun, gotStep)
	}
	if !reflect.DeepEqual(gotOut, payload) {
		t.Errorf("the workflow got %v, want the whole payload including system keys", gotOut)
	}
	row := wakeRow(t, db)
	if row.ActiveStepID != stepA || row.Seq != 5 || row.State != store.StateAdvancing {
		t.Errorf("row = (%q, %d, %q), want (%q, 5, ADVANCING)", row.ActiveStepID, row.Seq, row.State, stepA)
	}
	want := map[string]any{"in": "value", "userform": map[string]any{"name": "Alice"}}
	if !reflect.DeepEqual(row.Data, want) {
		t.Errorf("Data = %v, want %v: the submission goes under the namespace without system keys", row.Data, want)
	}
}

// A call for a step that is not the active one is turned away before anything happens.
func TestCompleteTaskStep_StaleStepIsRejectedWithoutWriting(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, wf := wakeUpFixture(t, db)
	called := false
	wf.taskDoneFunc = func(context.Context, string, string, string, map[string]any) error { called = true; return nil }

	err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepB, map[string]any{"x": 1})

	if !errors.Is(err, ErrStaleStep) {
		t.Fatalf("err = %v, want ErrStaleStep", err)
	}
	row := wakeRow(t, db)
	if called || row.State != "PENDING_USER" || row.Seq != 4 {
		t.Errorf("a stale call changed something: called=%v row=(%q, %d)", called, row.State, row.Seq)
	}
}

// The row can lag the workflow. If Temporal says the step is not pending, the call loses and writes
// nothing, even though the pre-check passed.
func TestCompleteTaskStep_StepNotPendingInTemporalIsStaleAndWritesNothing(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, wf := wakeUpFixture(t, db)
	wf.taskDoneFunc = func(context.Context, string, string, string, map[string]any) error {
		return errors.Join(engine.ErrActivationNotPending, errors.New("activity not found"))
	}

	err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"x": 1})

	if !errors.Is(err, ErrStaleStep) {
		t.Fatalf("err = %v, want ErrStaleStep", err)
	}
	row := wakeRow(t, db)
	if row.State != "PENDING_USER" || row.Seq != 4 {
		t.Errorf("row = (%q, %d): a losing call must not write", row.State, row.Seq)
	}
}

func TestCompleteTaskStep_OtherTemporalErrorsAreNotStale(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, wf := wakeUpFixture(t, db)
	wf.taskDoneFunc = func(context.Context, string, string, string, map[string]any) error {
		return errors.New("temporal down")
	}

	err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"x": 1})

	if err == nil || errors.Is(err, ErrStaleStep) {
		t.Fatalf("err = %v, want a plain failure the caller may retry", err)
	}
	if wakeRow(t, db).State != "PENDING_USER" {
		t.Error("nothing may be written when the workflow was not resumed")
	}
}

// Many callers race for one step: Temporal lets exactly one through, and only it writes.
func TestCompleteTaskStep_ConcurrentCallersOneWins(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, wf := wakeUpFixture(t, db)
	var won atomic.Bool
	wf.taskDoneFunc = func(context.Context, string, string, string, map[string]any) error {
		if !won.CompareAndSwap(false, true) {
			return engine.ErrActivationNotPending
		}
		return nil
	}

	const callers = 16
	var wg sync.WaitGroup
	var ok, stale atomic.Int32
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"n": i}); {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrStaleStep):
				stale.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if ok.Load() != 1 || stale.Load() != callers-1 {
		t.Fatalf("ok=%d stale=%d, want exactly one winner", ok.Load(), stale.Load())
	}
	if row := wakeRow(t, db); row.Seq != 5 || row.State != store.StateAdvancing {
		t.Errorf("row = (%d, %q), want it advanced exactly once", row.Seq, row.State)
	}
}

// The workflow can move on to the next step before the winner's persist. The persist then matches
// nothing, the row stays on the next step, and the call still succeeds.
func TestCompleteTaskStep_NextStepClaimedBeforePersistDropsThePersist(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, wf := wakeUpFixture(t, db)
	wf.taskDoneFunc = func(context.Context, string, string, string, map[string]any) error {
		_, err := db.ClaimStep(context.Background(), wakeTaskID, store.StepClaim{StepID: stepB, Seq: 5, State: store.StateStartingStep, Data: map[string]any{"b": 1}})
		return err
	}

	if err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"x": 1}); err != nil {
		t.Fatalf("err = %v, want success: the workflow accepted the step", err)
	}

	row := wakeRow(t, db)
	if row.ActiveStepID != stepB || row.Seq != 5 || row.State != store.StateStartingStep || !reflect.DeepEqual(row.Data, map[string]any{"b": 1}) {
		t.Errorf("row = (%q, %d, %q, %v), want B's claim untouched", row.ActiveStepID, row.Seq, row.State, row.Data)
	}
}

// A callback that lands while the plugin is still dispatching completes the step first. Its persist
// then wins, and the plugin's later render write for the same step matches nothing.
func TestCompleteTaskStep_CallbackBeforeTheRenderWriteWins(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, probe := startTaskStepManager(t, db)
	tm.taskWorkflowManager = &mockTemporalManager{taskDoneFunc: func(context.Context, string, string, string, map[string]any) error { return nil }}
	probe.whileRun = func(*store.TaskRecord) {
		if err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"name": "Alice"}); err != nil {
			t.Errorf("callback during dispatch: %v", err)
		}
	}

	if _, err := tm.StartTaskStep(context.Background(), engine.TaskPayload{
		WorkflowID: "task-wf", ActivationID: stepA, Seq: 4, TaskTemplateID: "generic_user_input",
	}); err == nil {
		t.Fatal("expected ErrResultPending from the suspended plugin")
	}

	row := wakeRow(t, db)
	if row.ActiveStepID != stepA || row.Seq != 5 || row.State != store.StateAdvancing {
		t.Errorf("row = (%q, %d, %q), want the callback's persist to stand (%q, 5, ADVANCING)", row.ActiveStepID, row.Seq, row.State, stepA)
	}
}

// startTaskStepManager is a task manager over db whose USER_INPUT plugin is a probe.
func startTaskStepManager(t *testing.T, db *safeMockTaskStore) (*TaskManager, *probePlugin) {
	t.Helper()
	db.InitTask(context.Background(), store.TaskRecord{TaskID: wakeTaskID, TaskType: "TEST", State: "STARTING", TaskWorkflowID: "task-wf", Data: map[string]any{}})
	tm, _, probe := startTaskStepFixtureOver(t, db)
	return tm, probe
}

// A failure to record an accepted submission must not be reported as a failure: the workflow has
// the step, and a retry could only be rejected as stale. The row catches up at the next claim.
type failingPersistStore struct{ *safeMockTaskStore }

func (failingPersistStore) PersistSubmission(context.Context, string, string, int64, map[string]any) (int64, error) {
	return 0, errors.New("db down")
}

func TestCompleteTaskStep_PersistFailureAfterAcceptanceIsNotAnError(t *testing.T) {
	db := failingPersistStore{newSafeMockTaskStore()}
	tm, wf := wakeUpFixture(t, db)
	wf.taskDoneFunc = func(context.Context, string, string, string, map[string]any) error { return nil }

	if err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"x": 1}); err != nil {
		t.Fatalf("err = %v, want nil: the workflow accepted the step", err)
	}
}

func TestCompleteTaskStepByToken(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, wf := wakeUpFixture(t, db)
	var gotStep string
	wf.taskDoneFunc = func(_ context.Context, _, _, stepID string, _ map[string]any) error { gotStep = stepID; return nil }

	token, err := callbacktoken.Encode(wakeTaskID, stepA)
	if err != nil {
		t.Fatal(err)
	}
	if err := tm.CompleteTaskStepByToken(context.Background(), token, map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if gotStep != stepA {
		t.Errorf("completed step %q, want %q: the token names the step", gotStep, stepA)
	}

	// A token for a step the task has moved past is stale, not malformed.
	old, _ := callbacktoken.Encode(wakeTaskID, stepB)
	if err := tm.CompleteTaskStepByToken(context.Background(), old, nil); !errors.Is(err, ErrStaleStep) {
		t.Errorf("err = %v, want ErrStaleStep", err)
	}

	if err := tm.CompleteTaskStepByToken(context.Background(), "not-a-token", nil); !errors.Is(err, callbacktoken.ErrInvalid) {
		t.Errorf("err = %v, want callbacktoken.ErrInvalid", err)
	}
}
