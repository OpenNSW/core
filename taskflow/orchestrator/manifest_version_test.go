// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
	"github.com/OpenNSW/core/taskflow/extensions"
	"github.com/OpenNSW/core/taskflow/renderer"
	"github.com/OpenNSW/core/taskflow/store"
	engine "github.com/OpenNSW/core/workflow"
)

// twoManifestSet has manifest v1 and v2 for the same IDs. v2 changes the task template's type
// and the user input step's output namespace, so a test can tell which version a call used.
// v2 is current.
func twoManifestSet(t *testing.T) *artifact.Set {
	t.Helper()
	m := testutil.MemLoader{
		"task_v1.json": []byte(`{"id":"apply","type":"TYPE_V1","workflow_id":"apply-flow","render_config_id":"apply-render"}`),
		"task_v2.json": []byte(`{"id":"apply","type":"TYPE_V2","workflow_id":"apply-flow","render_config_id":"apply-render"}`),
		"flow.json":    []byte(`{"id":"apply-flow","nodes":[]}`),
		"render.json":  []byte(`{}`),
		"step_v1.json": []byte(`{"id":"apply-form","task_type":"USER_INPUT","output_namespace":"form_v1","plugin_properties":{},"extensions":[{"id":"probe","phase":"PRE_RESUME"}]}`),
		"step_v2.json": []byte(`{"id":"apply-form","task_type":"USER_INPUT","output_namespace":"form_v2","plugin_properties":{},"extensions":[{"id":"probe","phase":"PRE_RESUME"}]}`),
		"manifest_v1.json": []byte(`{"artifacts":[
			{"id":"apply","kind":"task_template","path":"task_v1.json"},
			{"id":"apply-flow","kind":"workflow","path":"flow.json"},
			{"id":"apply-render","kind":"generic_template","path":"render.json"},
			{"id":"apply-form","kind":"subtask_template","path":"step_v1.json"}]}`),
		"manifest_v2.json": []byte(`{"artifacts":[
			{"id":"apply","kind":"task_template","path":"task_v2.json"},
			{"id":"apply-flow","kind":"workflow","path":"flow.json"},
			{"id":"apply-render","kind":"generic_template","path":"render.json"},
			{"id":"apply-form","kind":"subtask_template","path":"step_v2.json"}]}`),
	}
	set, err := artifact.LoadSet(context.Background(), m)
	if err != nil {
		t.Fatalf("LoadSet: %v", err)
	}
	if set.Current() != 2 {
		t.Fatalf("current = %d, want 2", set.Current())
	}
	return set
}

// A task records the version its parent workflow carries, uses that version's templates, and
// passes the version on to its own workflow.
func TestStartTask_PinsTheParentsManifestVersion(t *testing.T) {
	db := newSafeMockTaskStore()
	var started map[string]any
	wf := &mockTemporalManager{startWorkflowFunc: func(_ context.Context, _ string, _ engine.WorkflowDefinition, vars map[string]any) error {
		started = vars
		return nil
	}}
	tm := newTestTaskManager(db, twoManifestSet(t), wf, noopCallback)

	_, err := tm.StartTask(context.Background(), engine.TaskPayload{
		ManifestVersion: 1,
		WorkflowID:      "parent",
		ActivationID:    "6f1d2c3b-4a59-4e8d-9c7b-1a2b3c4d5e6f",
		TaskTemplateID:  "apply",
	})
	if !errors.Is(err, activity.ErrResultPending) {
		t.Fatalf("StartTask: %v", err)
	}
	rec, ok := db.GetTask(context.Background(), "6f1d2c3b-4a59-4e8d-9c7b-1a2b3c4d5e6f")
	if !ok {
		t.Fatal("task record not created")
	}
	if rec.ManifestVersion != 1 {
		t.Errorf("record ManifestVersion = %d, want 1", rec.ManifestVersion)
	}
	if rec.TaskType != "TYPE_V1" {
		t.Errorf("task type = %q, want TYPE_V1 from manifest v1 (v2 is current)", rec.TaskType)
	}
	if rec.TaskTemplateID != "apply" {
		t.Errorf("record TaskTemplateID = %q, want apply", rec.TaskTemplateID)
	}
	if started[engine.VarManifestVersion] != 1 {
		t.Errorf("task workflow started with %s = %v, want 1", engine.VarManifestVersion, started[engine.VarManifestVersion])
	}
}

// A task without a manifest version is refused before anything is written, and the error is not
// retryable: no retry can supply the version.
func TestStartTask_WithoutManifestVersionFailsWithoutRetry(t *testing.T) {
	db := newSafeMockTaskStore()
	startedWorkflow := false
	wf := &mockTemporalManager{startWorkflowFunc: func(context.Context, string, engine.WorkflowDefinition, map[string]any) error {
		startedWorkflow = true
		return nil
	}}
	tm := newTestTaskManager(db, twoManifestSet(t), wf, noopCallback)

	_, err := tm.StartTask(context.Background(), engine.TaskPayload{
		WorkflowID:     "parent",
		ActivationID:   "7a1d2c3b-4a59-4e8d-9c7b-1a2b3c4d5e6f",
		TaskTemplateID: "apply",
	})
	if !errors.Is(err, artifact.ErrUnknownManifestVersion) {
		t.Fatalf("expected ErrUnknownManifestVersion, got %v", err)
	}
	// Temporal reads retryability off the returned error itself, so it must not be wrapped.
	appErr, ok := err.(*temporal.ApplicationError)
	if !ok || !appErr.NonRetryable() {
		t.Errorf("expected an unwrapped non-retryable application error, got %T: %v", err, err)
	}
	if _, ok := db.GetTask(context.Background(), "7a1d2c3b-4a59-4e8d-9c7b-1a2b3c4d5e6f"); ok {
		t.Error("a task record was written")
	}
	if startedWorkflow {
		t.Error("the task workflow was started")
	}
}

// A version this replica has not loaded may still be published, so that error stays retryable.
func TestStartTask_UnpublishedVersionStaysRetryable(t *testing.T) {
	tm := newTestTaskManager(newSafeMockTaskStore(), twoManifestSet(t), &mockTemporalManager{}, noopCallback)
	_, err := tm.StartTask(context.Background(), engine.TaskPayload{
		ManifestVersion: 9,
		WorkflowID:      "parent",
		ActivationID:    "8b1d2c3b-4a59-4e8d-9c7b-1a2b3c4d5e6f",
		TaskTemplateID:  "apply",
	})
	if !errors.Is(err, artifact.ErrUnknownManifestVersion) {
		t.Fatalf("expected ErrUnknownManifestVersion, got %v", err)
	}
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) && appErr.NonRetryable() {
		t.Errorf("an unpublished version should be retryable, got %v", err)
	}
}

// Steps resolve through the task record's version, even when the step payload and the current
// manifest say otherwise, and the version reaches extensions through the context.
func TestSteps_UseTheTaskRecordsManifestVersion(t *testing.T) {
	const (
		taskID = "1c2d3e4f-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
		stepID = "2d3e4f5a-6b7c-4d8e-9f0a-1b2c3d4e5f6a"
	)
	ctx := context.Background()
	db := newSafeMockTaskStore()
	db.InitTask(ctx, store.TaskRecord{
		ManifestVersion: 1,
		TaskID:          taskID,
		TaskWorkflowID:  "task-wf",
		State:           "STARTING",
		Data:            map[string]any{},
	})

	var extensionVersion int
	var extensionSawVersion bool
	extReg := extensions.NewRegistry()
	extReg.Register("probe", &mockExtension{executeFunc: func(ctx context.Context, _ *store.TaskRecord, _ map[string]any, _ json.RawMessage) error {
		extensionVersion, extensionSawVersion = artifact.ManifestVersionFrom(ctx)
		return nil
	}})
	tm := NewTaskManager(db, twoManifestSet(t), newTestPluginsRegistry(), extReg, &mockTemporalManager{}, noopCallback, noopRenderer{})

	_, err := tm.StartTaskStep(ctx, engine.TaskPayload{
		ManifestVersion: 2, // ignored: the record is authoritative
		WorkflowID:      "task-wf",
		ActivationID:    stepID,
		Seq:             1,
		TaskTemplateID:  "apply-form",
	})
	if err != nil && !errors.Is(err, activity.ErrResultPending) {
		t.Fatalf("StartTaskStep: %v", err)
	}

	if err := tm.CompleteTaskStep(ctx, taskID, stepID, map[string]any{"name": "Ann"}); err != nil {
		t.Fatalf("CompleteTaskStep: %v", err)
	}
	rec, _ := db.GetTask(ctx, taskID)
	if _, ok := rec.Data["form_v1"]; !ok {
		t.Errorf("submission stored under %v, want the v1 step's namespace form_v1", keys(rec.Data))
	}
	if !extensionSawVersion || extensionVersion != 1 {
		t.Errorf("extension saw manifest version %d (present %v), want 1", extensionVersion, extensionSawVersion)
	}
}

type versionRecordingRenderer struct {
	version int
	ok      bool
}

func (r *versionRecordingRenderer) Render(ctx context.Context, _ json.RawMessage, _ renderer.Facts) (json.RawMessage, error) {
	r.version, r.ok = artifact.ManifestVersionFrom(ctx)
	return json.RawMessage(`{}`), nil
}

// Rendering a task hands the renderer the task's version, so its template provider resolves the
// render config's templates through it.
func TestGetTaskRenderInfo_PassesTheTasksManifestVersion(t *testing.T) {
	ctx := context.Background()
	db := newSafeMockTaskStore()
	db.InitTask(ctx, store.TaskRecord{ManifestVersion: 1, TaskID: "task-r", TaskTemplateID: "apply", State: "PENDING_USER", Data: map[string]any{}})
	r := &versionRecordingRenderer{}
	tm := NewTaskManager(db, twoManifestSet(t), newTestPluginsRegistry(), nil, &mockTemporalManager{}, noopCallback, r)

	if _, err := tm.GetTaskRenderInfo(ctx, "task-r"); err != nil {
		t.Fatalf("GetTaskRenderInfo: %v", err)
	}
	if !r.ok || r.version != 1 {
		t.Errorf("renderer saw manifest version %d (present %v), want 1", r.version, r.ok)
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A step of a task recorded without a version fails without retrying, before the plugin runs.
func TestStartTaskStep_RecordWithoutManifestVersionFailsWithoutRetry(t *testing.T) {
	ctx := context.Background()
	db := newSafeMockTaskStore()
	db.InitTask(ctx, store.TaskRecord{TaskID: "task-0", TaskWorkflowID: "task-wf-0", State: "STARTING", Data: map[string]any{}})
	tm := newTestTaskManager(db, twoManifestSet(t), &mockTemporalManager{}, noopCallback)

	_, err := tm.StartTaskStep(ctx, engine.TaskPayload{
		ManifestVersion: 2,
		WorkflowID:      "task-wf-0",
		ActivationID:    "3e4f5a6b-7c8d-4e9f-8a0b-1c2d3e4f5a6b",
		Seq:             1,
		TaskTemplateID:  "apply-form",
	})
	appErr, ok := err.(*temporal.ApplicationError)
	if !ok || !appErr.NonRetryable() || !errors.Is(err, artifact.ErrUnknownManifestVersion) {
		t.Fatalf("expected an unwrapped non-retryable ErrUnknownManifestVersion, got %T: %v", err, err)
	}
	if rec, _ := db.GetTask(ctx, "task-0"); rec.ActiveStepID != "" {
		t.Errorf("the step was claimed (active step %q)", rec.ActiveStepID)
	}
}
