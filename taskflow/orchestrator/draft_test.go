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

	"github.com/OpenNSW/core/taskflow/extensions"
	"github.com/OpenNSW/core/taskflow/store"
)

// draftProbe records what the draft step's extensions saw.
type draftProbe struct {
	preCommand  string
	preErr      error
	postCalls   atomic.Int32
	temporalHit atomic.Int32
}

// draftFixture is wakeUpFixture's task, on the draft-enabled step template draft_user_input
// (namespace "userform", draft command save_as_draft), holding data of another step and an
// internal key next to the step's prefilled input.
func draftFixture(t *testing.T, db store.TaskStore) (*TaskManager, *draftProbe) {
	t.Helper()
	ctx := context.Background()
	data := func() map[string]any {
		return map[string]any{
			"_task_id":     wakeTaskID,
			"reviewerform": map[string]any{"feedback": "fix the address"},
			"userform":     map[string]any{"applicant_name": "Prefilled"},
		}
	}
	db.InitTask(ctx, store.TaskRecord{ManifestVersion: 1, TaskID: wakeTaskID, TaskType: "TEST", State: "STARTING", TaskWorkflowID: "task-wf", Data: map[string]any{}})
	if _, err := db.ClaimStep(ctx, wakeTaskID, store.StepClaim{
		StepID: stepA, Seq: 4, ActiveTaskTemplateID: "draft_user_input", State: store.StateStartingStep, Data: data(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.WriteRenderState(ctx, wakeTaskID, stepA, 4, "PENDING_USER", data()); err != nil {
		t.Fatal(err)
	}

	probe := &draftProbe{}
	extReg := extensions.NewRegistry()
	extReg.Register("validator", &mockExtension{
		executeFunc: func(_ context.Context, _ *store.TaskRecord, payload map[string]any, _ json.RawMessage) error {
			probe.preCommand, _ = payload[CommandVar].(string)
			return probe.preErr
		},
	})
	extReg.Register("logger", &mockExtension{
		executeFunc: func(context.Context, *store.TaskRecord, map[string]any, json.RawMessage) error {
			probe.postCalls.Add(1)
			return nil
		},
	})
	wf := &mockTemporalManager{taskDoneFunc: func(context.Context, string, string, string, map[string]any) error {
		probe.temporalHit.Add(1)
		return nil
	}}
	return NewTaskManager(db, newTestRegistry(), newTestPluginsRegistry(), extReg, wf, noopCallback, noopRenderer{}), probe
}

func TestSaveDraft_WritesOnlyTheStepNamespaceAndDoesNotAdvance(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, probe := draftFixture(t, db)

	payload := map[string]any{"applicant_name": "Alice", "commodities": []any{}, "__command": "submit"}
	if err := tm.SaveDraft(context.Background(), wakeTaskID, stepA, payload); err != nil {
		t.Fatal(err)
	}

	row := wakeRow(t, db)
	if row.ActiveStepID != stepA || row.Seq != 4 || row.State != "PENDING_USER" {
		t.Errorf("row = (%q, %d, %q), want the step unchanged (%q, 4, PENDING_USER)", row.ActiveStepID, row.Seq, row.State, stepA)
	}
	want := map[string]any{
		"_task_id":     wakeTaskID,
		"reviewerform": map[string]any{"feedback": "fix the address"},
		"userform":     map[string]any{"applicant_name": "Alice", "commodities": []any{}},
	}
	if !reflect.DeepEqual(row.Data, want) {
		t.Errorf("Data = %v, want %v: only the namespace changes, without system keys", row.Data, want)
	}
	if probe.temporalHit.Load() != 0 {
		t.Error("a draft must not complete the step in the workflow")
	}
	if probe.preCommand != "save_as_draft" {
		t.Errorf("PRE_RESUME saw command %q, want the step's draft command whatever the payload carried", probe.preCommand)
	}
	if probe.postCalls.Load() != 0 {
		t.Error("POST_RESUME extensions must not run for a draft")
	}
}

// A draft can only ever write the step's own namespace: keys that name other slots land inside it.
func TestSaveDraft_CannotReachOtherSlots(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, _ := draftFixture(t, db)

	payload := map[string]any{"reviewerform": map[string]any{"feedback": "approved"}, "_task_id": "other"}
	if err := tm.SaveDraft(context.Background(), wakeTaskID, stepA, payload); err != nil {
		t.Fatal(err)
	}

	row := wakeRow(t, db)
	if row.Data["_task_id"] != wakeTaskID {
		t.Errorf("_task_id = %v, want it untouched", row.Data["_task_id"])
	}
	if got := row.Data["reviewerform"]; !reflect.DeepEqual(got, map[string]any{"feedback": "fix the address"}) {
		t.Errorf("reviewerform = %v, want it untouched", got)
	}
	if !reflect.DeepEqual(row.Data["userform"], payload) {
		t.Errorf("userform = %v, want the payload, under the namespace", row.Data["userform"])
	}
}

func TestSaveDraft_ThenSubmitReplacesTheDraft(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, _ := draftFixture(t, db)
	ctx := context.Background()

	if err := tm.SaveDraft(ctx, wakeTaskID, stepA, map[string]any{"applicant_name": "Al", "half": true}); err != nil {
		t.Fatal(err)
	}
	if err := tm.CompleteTaskStep(ctx, wakeTaskID, stepA, map[string]any{"applicant_name": "Alice", "__command": "submit"}); err != nil {
		t.Fatal(err)
	}

	row := wakeRow(t, db)
	if row.State != store.StateAdvancing || row.Seq != 5 {
		t.Errorf("row = (%q, %d), want (ADVANCING, 5)", row.State, row.Seq)
	}
	if want := map[string]any{"applicant_name": "Alice"}; !reflect.DeepEqual(row.Data["userform"], want) {
		t.Errorf("userform = %v, want %v", row.Data["userform"], want)
	}
	if err := tm.SaveDraft(ctx, wakeTaskID, stepA, map[string]any{"late": true}); !errors.Is(err, ErrStaleStep) {
		t.Errorf("a draft after the submission: err = %v, want ErrStaleStep", err)
	}
}

func TestSaveDraft_Rejections(t *testing.T) {
	cases := []struct {
		name           string
		taskID, stepID string
		setup          func(db *safeMockTaskStore)
		want           error
	}{
		{name: "no step ID", taskID: wakeTaskID, stepID: "", want: ErrStepIDRequired},
		{name: "unknown task", taskID: "nope", stepID: stepA, want: ErrTaskNotFound},
		{name: "another step", taskID: wakeTaskID, stepID: stepB, want: ErrStaleStep},
		{name: "not yet showing", taskID: wakeTaskID, stepID: stepA, setup: withState(store.StateStartingStep), want: ErrStaleStep},
		{name: "submitted", taskID: wakeTaskID, stepID: stepA, setup: withState(store.StateAdvancing), want: ErrStaleStep},
		{name: "completed", taskID: wakeTaskID, stepID: stepA, setup: withState(store.StateCompleted), want: ErrStaleStep},
		{name: "cancelled", taskID: wakeTaskID, stepID: stepA, setup: withState(store.StateCancelled), want: ErrStaleStep},
		{name: "on hold", taskID: wakeTaskID, stepID: stepA, setup: withState(store.StateOnHold), want: ErrStaleStep},
		{name: "drafts not enabled", taskID: wakeTaskID, stepID: stepA, setup: withTemplate("generic_user_input"), want: ErrDraftNotEnabled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := newSafeMockTaskStore()
			tm, probe := draftFixture(t, db)
			if c.setup != nil {
				c.setup(db)
			}
			before := wakeRow(t, db)

			err := tm.SaveDraft(context.Background(), c.taskID, c.stepID, map[string]any{"x": 1})

			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if !reflect.DeepEqual(wakeRow(t, db), before) || probe.temporalHit.Load() != 0 {
				t.Error("a rejected draft changed something")
			}
		})
	}
}

func TestSaveDraft_ExtensionDenialWritesNothing(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, probe := draftFixture(t, db)
	denied := errors.New("forbidden")
	probe.preErr = denied
	before := wakeRow(t, db)

	err := tm.SaveDraft(context.Background(), wakeTaskID, stepA, map[string]any{"x": 1})

	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want the extension's error", err)
	}
	if !reflect.DeepEqual(wakeRow(t, db), before) {
		t.Error("a denied draft changed the row")
	}
}

// The step can be submitted between the pre-check and the write. The guarded write then matches
// nothing, and the caller is told the step moved on.
type droppingDraftStore struct{ *safeMockTaskStore }

func (droppingDraftStore) WriteDraft(context.Context, string, string, int64, map[string]any) (int64, error) {
	return 0, nil
}

func TestSaveDraft_LostRaceIsStale(t *testing.T) {
	db := droppingDraftStore{newSafeMockTaskStore()}
	tm, _ := draftFixture(t, db)

	if err := tm.SaveDraft(context.Background(), wakeTaskID, stepA, map[string]any{"x": 1}); !errors.Is(err, ErrStaleStep) {
		t.Fatalf("err = %v, want ErrStaleStep", err)
	}
}

func withState(state string) func(db *safeMockTaskStore) {
	return func(db *safeMockTaskStore) {
		db.mu.Lock()
		defer db.mu.Unlock()
		r := db.tasks[wakeTaskID]
		r.State = state
		db.tasks[wakeTaskID] = r
	}
}

func withTemplate(id string) func(db *safeMockTaskStore) {
	return func(db *safeMockTaskStore) {
		db.mu.Lock()
		defer db.mu.Unlock()
		r := db.tasks[wakeTaskID]
		r.ActiveTaskTemplateID = id
		db.tasks[wakeTaskID] = r
	}
}
