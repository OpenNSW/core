// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.temporal.io/sdk/temporal"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/store"
	engine "github.com/OpenNSW/core/workflow"
)

// nonRetryable reports whether Temporal would stop retrying err. Temporal reads only the outermost
// error, so a non-retryable error wrapped under another one doesn't count.
func nonRetryable(err error) bool {
	appErr, ok := err.(*temporal.ApplicationError)
	return ok && appErr.NonRetryable()
}

// errPlugin fails with err.
type errPlugin struct{ err error }

func (p errPlugin) Execute(plugins.PluginContext, json.RawMessage) error { return p.err }

// retryRegistry holds a task template that doesn't parse, one whose workflow has a parallel gateway,
// and step templates for the FAILING plugin type and for a type nothing registers.
func retryRegistry() *artifact.Set {
	m := testutil.MemLoader{
		"task_invalid.json": []byte(`{`),
		"task_parallel.json": []byte(`{
			"id": "parallel_template", "type": "TEST",
			"workflow_id": "parallel_workflow", "render_config_id": "render_config"
		}`),
		"wf_parallel.json": []byte(`{
			"id": "parallel_workflow", "name": "Parallel", "version": 1,
			"nodes": [{"id": "split", "type": "GATEWAY", "gateway_type": "PARALLEL_SPLIT"}]
		}`),
		"render_config.json": []byte(`{}`),
		"subtask_failing.json": []byte(`{
			"id": "failing_step", "task_type": "FAILING", "output_namespace": "out", "plugin_properties": {}
		}`),
		"subtask_unregistered.json": []byte(`{
			"id": "unregistered_step", "task_type": "NOBODY", "output_namespace": "out", "plugin_properties": {}
		}`),
	}
	m[artifact.ManifestPath(1)] = []byte(`{"artifacts": [
		{"id": "invalid_template", "kind": "task_template", "path": "task_invalid.json"},
		{"id": "parallel_template", "kind": "task_template", "path": "task_parallel.json"},
		{"id": "parallel_workflow", "kind": "workflow", "path": "wf_parallel.json"},
		{"id": "render_config", "kind": "generic_template", "path": "render_config.json"},
		{"id": "failing_step", "kind": "subtask_template", "path": "subtask_failing.json"},
		{"id": "unregistered_step", "kind": "subtask_template", "path": "subtask_unregistered.json"}
	]}`)
	set, err := artifact.LoadSet(context.Background(), m)
	if err != nil {
		panic(err)
	}
	return set
}

// retryTaskManager is a task manager over retryRegistry, whose FAILING plugin returns pluginErr, and
// whose store holds the task of the task workflow "task-wf".
func retryTaskManager(t *testing.T, pluginErr error) *TaskManager {
	t.Helper()
	db := newSafeMockTaskStore()
	db.InitTask(context.Background(), store.TaskRecord{
		TaskID: "task-1", TaskType: "TEST", State: "STARTING", TaskWorkflowID: "task-wf", ManifestVersion: 1,
	})
	pr := plugins.NewRegistry()
	if err := pr.Register("FAILING", errPlugin{err: pluginErr}); err != nil {
		t.Fatal(err)
	}
	return NewTaskManager(db, retryRegistry(), pr, nil, &mockTemporalManager{}, noopCallback, noopRenderer{})
}

func TestStartTask_PermanentErrorsAreNotRetried(t *testing.T) {
	for name, payload := range map[string]engine.TaskPayload{
		"unknown template": {WorkflowID: "parent-wf", ActivationID: stepA, TaskTemplateID: "does_not_exist", ManifestVersion: 1},
		"invalid template": {WorkflowID: "parent-wf", ActivationID: stepA, TaskTemplateID: "invalid_template", ManifestVersion: 1},
		"parallel gateway": {WorkflowID: "parent-wf", ActivationID: stepA, TaskTemplateID: "parallel_template", ManifestVersion: 1},
		"no step ID":       {WorkflowID: "parent-wf", TaskTemplateID: "parallel_template", ManifestVersion: 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := retryTaskManager(t, nil).StartTask(context.Background(), payload)
			if !nonRetryable(err) {
				t.Errorf("err = %v, want a non-retryable error", err)
			}
		})
	}
}

// A failure that may clear (here, starting the task workflow) is left to the retry policy.
func TestStartTask_TransientErrorIsRetried(t *testing.T) {
	tm := newTestTaskManager(newSafeMockTaskStore(), newTestRegistry(), &mockTemporalManager{
		startWorkflowFunc: func(context.Context, string, engine.WorkflowDefinition, map[string]any) error {
			return errors.New("temporal unavailable")
		},
	}, noopCallback)

	_, err := tm.StartTask(context.Background(), engine.TaskPayload{WorkflowID: "parent-wf", ActivationID: stepA, TaskTemplateID: "test_template", ManifestVersion: 1})
	if err == nil || nonRetryable(err) {
		t.Errorf("err = %v, want a retryable error", err)
	}
}

func TestStartTaskStep_PermanentErrorsAreNotRetried(t *testing.T) {
	for name, payload := range map[string]engine.TaskPayload{
		"unknown step template": {WorkflowID: "task-wf", ActivationID: stepA, Seq: 1, TaskTemplateID: "does_not_exist"},
		"unregistered plugin":   {WorkflowID: "task-wf", ActivationID: stepA, Seq: 1, TaskTemplateID: "unregistered_step"},
		"no step ID":            {WorkflowID: "task-wf", Seq: 1, TaskTemplateID: "failing_step"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := retryTaskManager(t, nil).StartTaskStep(context.Background(), payload)
			if !nonRetryable(err) {
				t.Errorf("err = %v, want a non-retryable error", err)
			}
		})
	}
}

// A plugin's Permanent error stays non-retryable although StartTaskStep wraps it, and keeps its type.
func TestStartTaskStep_KeepsAPluginsPermanentError(t *testing.T) {
	cause := errors.New("missing consignment id")
	tm := retryTaskManager(t, plugins.Permanent(plugins.ErrorTypeInput, cause))

	_, err := tm.StartTaskStep(context.Background(), engine.TaskPayload{WorkflowID: "task-wf", ActivationID: stepA, Seq: 1, TaskTemplateID: "failing_step"})
	if !nonRetryable(err) {
		t.Fatalf("err = %v, want a non-retryable error", err)
	}
	if appErr := err.(*temporal.ApplicationError); appErr.Type() != plugins.ErrorTypeInput {
		t.Errorf("type = %q, want %q", appErr.Type(), plugins.ErrorTypeInput)
	}
	want := `[StartTaskStep] plugin for plugin type "FAILING" execution failed: missing consignment id (type: InvalidInput, retryable: false)`
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

func TestStartTaskStep_TransientErrorsAreRetried(t *testing.T) {
	t.Run("plugin error", func(t *testing.T) {
		tm := retryTaskManager(t, errors.New("agency unavailable"))
		_, err := tm.StartTaskStep(context.Background(), engine.TaskPayload{WorkflowID: "task-wf", ActivationID: stepA, Seq: 1, TaskTemplateID: "failing_step"})
		if err == nil || nonRetryable(err) {
			t.Errorf("err = %v, want a retryable error", err)
		}
	})
	// The store reports a database error as a missing record, so a missing record may clear.
	t.Run("no task record", func(t *testing.T) {
		tm := retryTaskManager(t, nil)
		_, err := tm.StartTaskStep(context.Background(), engine.TaskPayload{WorkflowID: "other-wf", ActivationID: stepA, Seq: 1, TaskTemplateID: "failing_step"})
		if err == nil || nonRetryable(err) {
			t.Errorf("err = %v, want a retryable error", err)
		}
	})
}
