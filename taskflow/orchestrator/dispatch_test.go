// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/OpenNSW/core/artifact/adapter/types"
	"github.com/OpenNSW/core/taskflow/extensions"
	"github.com/OpenNSW/core/taskflow/store"
)

// seen is what one extension run received.
type seen struct {
	inv    extensions.Invocation
	hasInv bool
	record store.TaskRecord
}

// recordingExtension records every run and returns err.
type recordingExtension struct {
	runs []seen
	err  error
}

func (e *recordingExtension) Execute(ctx context.Context, record *store.TaskRecord, _ map[string]any, _ json.RawMessage) error {
	inv, ok := extensions.InvocationFrom(ctx)
	e.runs = append(e.runs, seen{inv: inv, hasInv: ok, record: record.DeepCopy()})
	// Mutations must not reach the row (the read-only contract).
	if record.Data != nil {
		record.Data["mutated"] = true
	}
	return e.err
}

// dispatchFixture is wakeUpFixture's task (step A, seq 4) on templateID, behind a TaskManager whose
// Dispatcher runs inline and counts its dispatches. "validator" (PRE_RESUME) and "logger"
// (POST_RESUME) are registered as recording extensions.
func dispatchFixture(t *testing.T, templateID string) (*TaskManager, *safeMockTaskStore, *recordingExtension, *recordingExtension, *int) {
	t.Helper()
	db := newSafeMockTaskStore()
	wakeUpFixture(t, db)
	withTemplate(templateID)(db)

	validator, logger := &recordingExtension{}, &recordingExtension{}
	extReg := extensions.NewRegistry()
	extReg.Register("validator", validator)
	extReg.Register("logger", logger)

	dispatches := new(int)
	sync := DispatcherFunc(func(ctx context.Context, run func(context.Context)) {
		*dispatches++
		run(ctx)
	})
	tm := NewTaskManager(db, newTestRegistry(), newTestPluginsRegistry(), extReg, &mockTemporalManager{}, noopCallback, noopRenderer{}, WithDispatcher(sync))
	return tm, db, validator, logger, dispatches
}

func TestCompleteTaskStep_ExtensionsReceiveTheirInvocation(t *testing.T) {
	tm, db, validator, logger, dispatches := dispatchFixture(t, "generic_user_input_with_extensions")

	if err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"age": 20.0}); err != nil {
		t.Fatalf("CompleteTaskStep: %v", err)
	}

	if len(validator.runs) != 1 || len(logger.runs) != 1 || *dispatches != 1 {
		t.Fatalf("validator runs = %d, logger runs = %d, dispatches = %d; want 1 each", len(validator.runs), len(logger.runs), *dispatches)
	}
	want := map[*recordingExtension]extensions.Invocation{
		validator: {Phase: types.PhasePreResume, IdempotencyKey: wakeTaskID + "/" + stepA + "/4/PRE_RESUME"},
		// The row has advanced to seq 5 by now, but the key names the run of the step: seq 4.
		logger: {Phase: types.PhasePostResume, IdempotencyKey: wakeTaskID + "/" + stepA + "/4/POST_RESUME"},
	}
	for ext, inv := range want {
		if got := ext.runs[0]; !got.hasInv || got.inv != inv {
			t.Errorf("invocation = %+v (present %v), want %+v", got.inv, got.hasInv, inv)
		}
	}

	// The post-resume extension sees the submission; neither extension's mutation reaches the row.
	if form, _ := logger.runs[0].record.Data["form"].(map[string]any); form["age"] != 20.0 {
		t.Errorf("post-resume record data = %v, want the submission under \"form\"", logger.runs[0].record.Data)
	}
	if _, mutated := wakeRow(t, db).Data["mutated"]; mutated {
		t.Error("an extension's mutation of the record reached the row")
	}
}

func TestCompleteTaskStep_RefusedGuardDispatchesNothing(t *testing.T) {
	tm, db, validator, logger, dispatches := dispatchFixture(t, "generic_user_input_with_extensions")
	validator.err = errors.New("refused")
	before := wakeRow(t, db)

	if err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"age": 20.0}); err == nil {
		t.Fatal("CompleteTaskStep succeeded, want the guard's refusal")
	}
	if *dispatches != 0 || len(logger.runs) != 0 {
		t.Errorf("dispatches = %d, logger runs = %d; a refused step must notify nothing", *dispatches, len(logger.runs))
	}
	if !reflect.DeepEqual(wakeRow(t, db), before) {
		t.Error("a refused step changed the row")
	}
}

// A step that declares no post-resume extension does not dispatch an empty run.
func TestCompleteTaskStep_NothingDeclaredDispatchesNothing(t *testing.T) {
	tm, _, _, _, dispatches := dispatchFixture(t, "generic_user_input")

	if err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"x": 1}); err != nil {
		t.Fatalf("CompleteTaskStep: %v", err)
	}
	if *dispatches != 0 {
		t.Errorf("dispatches = %d, want 0", *dispatches)
	}
}

func TestSaveDraft_GuardReceivesItsInvocation(t *testing.T) {
	tm, _, validator, logger, dispatches := dispatchFixture(t, "draft_user_input")

	if err := tm.SaveDraft(context.Background(), wakeTaskID, stepA, map[string]any{"x": 1}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	want := extensions.Invocation{Phase: types.PhasePreResume, IdempotencyKey: wakeTaskID + "/" + stepA + "/4/PRE_RESUME"}
	if len(validator.runs) != 1 || validator.runs[0].inv != want {
		t.Errorf("validator runs = %+v, want one with %+v", validator.runs, want)
	}
	// A draft never runs the post-resume extensions.
	if *dispatches != 0 || len(logger.runs) != 0 {
		t.Errorf("dispatches = %d, logger runs = %d; want 0", *dispatches, len(logger.runs))
	}
}

func TestFire_NoRegistryRunsNothing(t *testing.T) {
	tm, _, _, _, dispatches := dispatchFixture(t, "generic_user_input_with_extensions")
	tm.extensionsRegistry = nil
	cfgs := []types.ExtensionConfig{{ID: "logger", Phase: types.PhasePostResume}}

	if err := tm.fire(context.Background(), types.PhasePostResume, occurrence{}, store.TaskRecord{}, nil, cfgs); err != nil {
		t.Fatalf("fire: %v", err)
	}
	if *dispatches != 0 {
		t.Errorf("dispatches = %d, want 0", *dispatches)
	}
}

func TestFire_UnknownPhase(t *testing.T) {
	tm, _, _, _, _ := dispatchFixture(t, "generic_user_input_with_extensions")
	cfgs := []types.ExtensionConfig{{ID: "logger", Phase: "NOT_A_PHASE"}}

	if err := tm.fire(context.Background(), "NOT_A_PHASE", occurrence{}, store.TaskRecord{}, nil, cfgs); err == nil {
		t.Error("fire accepted an unknown phase")
	}
}

// A notify phase outlives the request that fired it: the dispatched context is never cancelled.
func TestFire_DispatchedContextIsNotCancelled(t *testing.T) {
	tm, _, _, _, _ := dispatchFixture(t, "generic_user_input_with_extensions")
	dispatchedErr := errors.New("not dispatched")
	tm.dispatcher = DispatcherFunc(func(ctx context.Context, run func(context.Context)) {
		dispatchedErr = ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfgs := []types.ExtensionConfig{{ID: "logger", Phase: types.PhasePostResume}}

	if err := tm.fire(ctx, types.PhasePostResume, occurrence{}, store.TaskRecord{}, nil, cfgs); err != nil {
		t.Fatalf("fire: %v", err)
	}
	if dispatchedErr != nil {
		t.Errorf("dispatched context err = %v, want nil", dispatchedErr)
	}
}
