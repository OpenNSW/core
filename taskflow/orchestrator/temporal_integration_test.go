// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
	"github.com/OpenNSW/core/taskflow/store"
	engine "github.com/OpenNSW/core/workflow"
)

// This test drives a TaskManager through a real task workflow on a real Temporal server: two
// user-input steps, with stale and duplicate submissions in between. Set TEMPORAL_TEST_ADDRESS to
// run it, for example against `temporal server start-dev`; it is skipped otherwise.
func TestTaskManagerEndToEndOnTemporal(t *testing.T) {
	addr := os.Getenv("TEMPORAL_TEST_ADDRESS")
	if addr == "" {
		t.Skip("TEMPORAL_TEST_ADDRESS is not set")
	}
	c, err := client.Dial(client.Options{HostPort: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	registry := twoStepRegistry()
	db := newSafeMockTaskStore()

	var tm *TaskManager
	wfm := engine.NewTemporalManager(c, "default", "tm-it-"+uuid.NewString(),
		func(p engine.TaskPayload) (map[string]any, error) { return tm.StartTaskStep(context.Background(), p) },
		func(c engine.WorkflowCompletion) error { return tm.HandleTaskCompletion(context.Background(), c) },
	)
	var parentWoken atomic.Int32
	var parentStep atomic.Value
	tm = NewTaskManager(db, registry, newTestPluginsRegistry(), nil, wfm,
		func(_, step string, _ map[string]any) error {
			parentWoken.Add(1)
			parentStep.Store(step)
			return nil
		},
		noopRenderer{})
	if err := wfm.StartWorker(); err != nil {
		t.Fatal(err)
	}
	defer wfm.StopWorker()

	ctx := context.Background()
	parentStepID := uuid.NewString()
	if _, err := tm.StartTask(ctx, engine.TaskPayload{
		WorkflowID: "parent-wf", RunID: "parent-run", NodeID: "parent-node", ActivationID: parentStepID, Seq: 1,
		TaskTemplateID: "test_template", RootWorkflowID: "parent-wf",
	}); err != nil && !errors.Is(err, activity.ErrResultPending) {
		t.Fatal(err)
	}
	taskID := parentStepID

	// waitFor polls the row until cond holds.
	waitFor := func(what string, cond func(store.TaskRecord) bool) store.TaskRecord {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if row, ok := db.GetTask(ctx, taskID); ok && cond(row) {
				return row
			}
			time.Sleep(50 * time.Millisecond)
		}
		row, _ := db.GetTask(ctx, taskID)
		t.Fatalf("timed out waiting for %s; row = (%q, %d, %q)", what, row.ActiveStepID, row.Seq, row.State)
		return store.TaskRecord{}
	}

	// Step 1 is claimed and waiting for the user.
	first := waitFor("step 1 pending", func(r store.TaskRecord) bool { return r.State == "PENDING_USER" && r.ActiveStepID != "" })
	if first.Seq != 1 {
		t.Errorf("step 1 seq = %d, want 1", first.Seq)
	}

	// Submitting with a step ID that is not the active one is rejected before anything happens.
	if err := tm.CompleteTaskStep(ctx, taskID, uuid.NewString(), map[string]any{"x": 1}); !errors.Is(err, ErrStaleStep) {
		t.Fatalf("submit for an unknown step: err = %v, want ErrStaleStep", err)
	}

	// The real submission is accepted, and the row moves on to step 2.
	if err := tm.CompleteTaskStep(ctx, taskID, first.ActiveStepID, map[string]any{"name": "Alice"}); err != nil {
		t.Fatalf("submit step 1: %v", err)
	}
	second := waitFor("step 2 pending", func(r store.TaskRecord) bool {
		return r.State == "PENDING_USER" && r.ActiveStepID != "" && r.ActiveStepID != first.ActiveStepID
	})
	if second.Seq != 2 {
		t.Errorf("step 2 seq = %d, want 2", second.Seq)
	}

	// A late or duplicate submission for step 1 must not touch step 2, however it arrives.
	if err := tm.CompleteTaskStep(ctx, taskID, first.ActiveStepID, map[string]any{"name": "Mallory"}); !errors.Is(err, ErrStaleStep) {
		t.Fatalf("duplicate submit for step 1: err = %v, want ErrStaleStep", err)
	}
	if row, _ := db.GetTask(ctx, taskID); row.ActiveStepID != second.ActiveStepID || row.State != "PENDING_USER" {
		t.Fatalf("a stale submission changed the row: (%q, %q)", row.ActiveStepID, row.State)
	}

	// Step 2 completes the task; the parent is woken exactly once, with the parent's step ID.
	if err := tm.CompleteTaskStep(ctx, taskID, second.ActiveStepID, map[string]any{"name": "Bob"}); err != nil {
		t.Fatalf("submit step 2: %v", err)
	}
	done := waitFor("task completed", func(r store.TaskRecord) bool { return r.State == store.StateCompleted })
	if done.ActiveStepID != second.ActiveStepID {
		t.Errorf("completed on step %q, want %q", done.ActiveStepID, second.ActiveStepID)
	}
	if got := parentWoken.Load(); got != 1 {
		t.Errorf("parent woken %d times, want 1", got)
	}
	if got, _ := parentStep.Load().(string); got != parentStepID {
		t.Errorf("parent woken with step %q, want %q", got, parentStepID)
	}
	if done.Seq != 3 {
		t.Errorf("completion seq = %d, want 3 (two steps, then the end)", done.Seq)
	}
}

// twoStepRegistry is a registry whose task template runs two user-input steps in a row.
func twoStepRegistry() *artifact.Registry {
	m := testutil.MemLoader{
		"task_test_template.json": []byte(`{"id": "test_template", "type": "TEST", "workflow_id": "two_step_v1", "render_config_id": "test_render_config"}`),
		"wf_two_step.json": []byte(`{
			"id": "two_step_v1", "name": "Two steps", "version": 1,
			"edges": [
				{"id": "e1", "source_id": "start", "target_id": "form1"},
				{"id": "e2", "source_id": "form1", "target_id": "form2"},
				{"id": "e3", "source_id": "form2", "target_id": "end"}
			],
			"nodes": [
				{"id": "start", "type": "START"},
				{"id": "form1", "type": "TASK", "task_template_id": "generic_user_input"},
				{"id": "form2", "type": "TASK", "task_template_id": "generic_user_input"},
				{"id": "end", "type": "END"}
			]
		}`),
		"generic_render_config.json":      []byte(`{}`),
		"subtask_generic_user_input.json": []byte(`{"id": "generic_user_input", "task_type": "USER_INPUT", "output_namespace": "userform", "plugin_properties": {}}`),
	}
	reg := artifact.NewRegistry(m)
	reg.RegisterArtifact("test_template", "task_template", "", "task_test_template.json")
	reg.RegisterArtifact("two_step_v1", "workflow", "", "wf_two_step.json")
	reg.RegisterArtifact("test_render_config", "generic_template", "", "generic_render_config.json")
	reg.RegisterArtifact("generic_user_input", "subtask_template", "", "subtask_generic_user_input.json")
	return reg
}
