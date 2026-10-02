// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	engine "github.com/OpenNSW/core/workflow"

	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/store"
)

const (
	stepA = "0a0a0a0a-0000-4000-8000-00000000000a"
	stepB = "0b0b0b0b-0000-4000-8000-00000000000b"
)

// probePlugin stands in for a USER_INPUT plugin: it counts its runs, lets a test act while the
// plugin is "dispatching", and then suspends like a real one waiting for the user.
type probePlugin struct {
	runs     atomic.Int32
	whileRun func(rec *store.TaskRecord)
}

func (p *probePlugin) Execute(ctx plugins.PluginContext, _ json.RawMessage) error {
	p.runs.Add(1)
	if p.whileRun != nil {
		p.whileRun(ctx.Record)
	}
	ctx.Record.State = "PENDING_USER"
	return plugins.ErrSuspended
}

// startTaskStepFixture is a task whose task workflow is "task-wf" and whose row has not started a
// step, with a probe plugin registered as USER_INPUT.
func startTaskStepFixture(t *testing.T) (*TaskManager, *safeMockTaskStore, *probePlugin) {
	t.Helper()
	db := newSafeMockTaskStore()
	db.SaveTask(context.Background(), store.TaskRecord{
		TaskID: "task-1", TaskType: "TEST", State: "STARTING", TaskWorkflowID: "task-wf",
		Data: map[string]any{"old": "value"},
	})
	probe := &probePlugin{}
	pr := plugins.NewRegistry()
	if err := pr.Register("USER_INPUT", probe); err != nil {
		t.Fatal(err)
	}
	tm := NewTaskManager(db, newTestRegistry(), pr, nil, &mockTemporalManager{}, noopCallback, noopRenderer{})
	return tm, db, probe
}

func stepPayload(step string, seq int64, inputs map[string]any) engine.TaskPayload {
	return engine.TaskPayload{
		WorkflowID: "task-wf", RunID: "run-1", NodeID: "form", ActivationID: step, Seq: seq,
		TaskTemplateID: "generic_user_input", Inputs: inputs,
	}
}

func mustRow(t *testing.T, db *safeMockTaskStore) store.TaskRecord {
	t.Helper()
	row, ok := db.GetTask(context.Background(), "task-1")
	if !ok {
		t.Fatal("task row missing")
	}
	return row
}

func TestStartTaskStep_ClaimsTheStepBeforeThePluginRunsAndRendersAfter(t *testing.T) {
	tm, db, probe := startTaskStepFixture(t)

	var seenByPlugin store.TaskRecord
	probe.whileRun = func(_ *store.TaskRecord) { seenByPlugin = mustRow(t, db) }

	_, err := tm.StartTaskStep(context.Background(), stepPayload(stepA, 4, map[string]any{"form.name": "Alice"}))
	if !errors.Is(err, activity.ErrResultPending) {
		t.Fatalf("err = %v, want ErrResultPending", err)
	}

	if seenByPlugin.ActiveStepID != stepA || seenByPlugin.Seq != 4 || seenByPlugin.State != store.StateStartingStep {
		t.Errorf("row while the plugin ran = (%q, %d, %q), want the claimed step", seenByPlugin.ActiveStepID, seenByPlugin.Seq, seenByPlugin.State)
	}
	row := mustRow(t, db)
	if row.ActiveStepID != stepA || row.Seq != 4 || row.State != "PENDING_USER" {
		t.Errorf("row after = (%q, %d, %q), want (%q, 4, PENDING_USER)", row.ActiveStepID, row.Seq, row.State, stepA)
	}
	if row.ActiveTaskTemplateID != "generic_user_input" {
		t.Errorf("ActiveTaskTemplateID = %q", row.ActiveTaskTemplateID)
	}
}

// The claim sets the data from the step's inputs; what an earlier step left on the row is not kept.
func TestStartTaskStep_DataIsSetFromInputsNotMerged(t *testing.T) {
	tm, db, _ := startTaskStepFixture(t)

	_, _ = tm.StartTaskStep(context.Background(), stepPayload(stepA, 1, map[string]any{"form.name": "Alice"}))

	want := map[string]any{"form": map[string]any{"name": "Alice"}}
	if got := mustRow(t, db).Data; !reflect.DeepEqual(got, want) {
		t.Errorf("Data = %v, want %v", got, want)
	}
}

// A retry of the same step (Temporal re-running the activity, or an admin retry) claims it again.
func TestStartTaskStep_RetryOfTheSameStepClaimsAgain(t *testing.T) {
	tm, db, probe := startTaskStepFixture(t)

	for i := 0; i < 2; i++ {
		if _, err := tm.StartTaskStep(context.Background(), stepPayload(stepA, 4, nil)); !errors.Is(err, activity.ErrResultPending) {
			t.Fatalf("attempt %d: err = %v, want ErrResultPending", i+1, err)
		}
	}
	if probe.runs.Load() != 2 {
		t.Errorf("plugin ran %d times, want 2", probe.runs.Load())
	}
	if row := mustRow(t, db); row.ActiveStepID != stepA || row.Seq != 4 {
		t.Errorf("row = (%q, %d)", row.ActiveStepID, row.Seq)
	}
}

// A late attempt for a step that is already over must not run the plugin, which would make an
// outbound call for a step that is gone, and must not touch the row.
func TestStartTaskStep_LateAttemptOfAnEarlierStepDoesNothing(t *testing.T) {
	tm, db, probe := startTaskStepFixture(t)
	if _, err := db.ClaimStep(context.Background(), "task-1", store.StepClaim{StepID: stepB, Seq: 5, State: store.StateStartingStep, Data: map[string]any{"b": 1}}); err != nil {
		t.Fatal(err)
	}

	_, err := tm.StartTaskStep(context.Background(), stepPayload(stepA, 4, map[string]any{"late": true}))

	if !errors.Is(err, ErrStaleStep) {
		t.Fatalf("err = %v, want ErrStaleStep", err)
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Errorf("err = %v, want a non-retryable application error so Temporal does not retry it", err)
	}
	if probe.runs.Load() != 0 {
		t.Errorf("plugin ran %d times for a stale step, want 0", probe.runs.Load())
	}
	row := mustRow(t, db)
	if row.ActiveStepID != stepB || row.Seq != 5 || !reflect.DeepEqual(row.Data, map[string]any{"b": 1}) {
		t.Errorf("row changed by a stale attempt: (%q, %d, %v)", row.ActiveStepID, row.Seq, row.Data)
	}
}

// A fast callback completes the step and the next one claims the row while the first activity is
// still inside its plugin. Its render write must then be dropped, not applied over the new step.
func TestStartTaskStep_RenderWriteAfterANewerClaimIsDropped(t *testing.T) {
	tm, db, probe := startTaskStepFixture(t)
	probe.whileRun = func(_ *store.TaskRecord) {
		if _, err := db.ClaimStep(context.Background(), "task-1", store.StepClaim{StepID: stepB, Seq: 5, State: store.StateStartingStep, Data: map[string]any{"b": 1}}); err != nil {
			t.Error(err)
		}
	}

	_, err := tm.StartTaskStep(context.Background(), stepPayload(stepA, 4, nil))
	if !errors.Is(err, activity.ErrResultPending) {
		t.Fatalf("err = %v, want ErrResultPending: a dropped write is not a failure", err)
	}

	row := mustRow(t, db)
	if row.ActiveStepID != stepB || row.Seq != 5 || row.State != store.StateStartingStep {
		t.Errorf("row = (%q, %d, %q), want step B untouched", row.ActiveStepID, row.Seq, row.State)
	}
}

func TestStartTaskStep_RequiresAStepID(t *testing.T) {
	tm, db, probe := startTaskStepFixture(t)

	if _, err := tm.StartTaskStep(context.Background(), stepPayload("", 1, nil)); err == nil {
		t.Fatal("expected an error for a payload without a step ID")
	}
	if probe.runs.Load() != 0 || mustRow(t, db).ActiveStepID != "" {
		t.Error("a payload without a step ID must change nothing")
	}
}

// Nothing addresses a step before the first claim, so there is nothing to poll for.
func TestCompleteTaskStep_BeforeAnyStepIsClaimedFails(t *testing.T) {
	tm, _, _ := startTaskStepFixture(t)

	if err := tm.CompleteTaskStep(context.Background(), "task-1", map[string]any{"x": 1}); err == nil {
		t.Fatal("expected an error: no step has been claimed")
	}
}
