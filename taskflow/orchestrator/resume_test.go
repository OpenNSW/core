// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
	"github.com/OpenNSW/core/taskflow/extensions"
	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/store"
)

// resumingPlugin is a plugin that implements plugins.Resumer. Each test sets resume.
type resumingPlugin struct {
	calls  int
	resume func(ctx plugins.PluginContext, config json.RawMessage, payload map[string]any) (map[string]any, error)
}

func (p *resumingPlugin) Execute(plugins.PluginContext, json.RawMessage) error {
	return plugins.ErrSuspended
}

func (p *resumingPlugin) Resume(ctx plugins.PluginContext, config json.RawMessage, payload map[string]any) (map[string]any, error) {
	p.calls++
	return p.resume(ctx, config, payload)
}

// resumeHarness is a task waiting on one step, with what the workflow received from it.
type resumeHarness struct {
	tm     *TaskManager
	db     *safeMockTaskStore
	plugin *resumingPlugin
	// gateErr is what the step's PRE_RESUME extension "gate" returns.
	gateErr error
	// sent is the payload the workflow received; completions counts the calls.
	sent        map[string]any
	completions int
}

// resumeArtifacts holds two step templates: resuming_step, run by the RESUMING plugin (namespace
// "pay", a draft command, the PRE_RESUME extension "gate"), and orphan_step, whose plugin type is
// not registered.
func resumeArtifacts(t *testing.T) *artifact.Set {
	t.Helper()
	m := testutil.MemLoader{
		"task.json":   []byte(`{"id":"pay-task","type":"TEST","workflow_id":"pay-flow","render_config_id":"pay-render"}`),
		"flow.json":   []byte(`{"id":"pay-flow","nodes":[]}`),
		"render.json": []byte(`{}`),
		"resuming_step.json": []byte(`{"id":"resuming_step","task_type":"RESUMING","output_namespace":"pay",
			"plugin_properties":{"fee":"5000"},"draft":{"command":"save_as_draft"},
			"extensions":[{"id":"gate","phase":"PRE_RESUME"}]}`),
		"orphan_step.json": []byte(`{"id":"orphan_step","task_type":"NOBODY","output_namespace":"out","plugin_properties":{}}`),
	}
	m[artifact.ManifestPath(1)] = []byte(`{"artifacts":[
		{"id":"pay-task","kind":"task_template","path":"task.json"},
		{"id":"pay-flow","kind":"workflow","path":"flow.json"},
		{"id":"pay-render","kind":"generic_template","path":"render.json"},
		{"id":"resuming_step","kind":"subtask_template","path":"resuming_step.json"},
		{"id":"orphan_step","kind":"subtask_template","path":"orphan_step.json"}]}`)
	set, err := artifact.LoadSet(context.Background(), m)
	if err != nil {
		t.Fatalf("LoadSet: %v", err)
	}
	return set
}

// newResumeHarness starts a task whose step A (seq 4, template stepTemplateID) waits in
// PENDING_PAYMENT, holding the reference its plugin stored under "pay".
func newResumeHarness(t *testing.T, stepTemplateID string) *resumeHarness {
	t.Helper()
	ctx := context.Background()
	h := &resumeHarness{db: newSafeMockTaskStore(), plugin: &resumingPlugin{}}

	data := func() map[string]any {
		return map[string]any{"in": "value", "pay": map[string]any{"reference_number": "REF-1"}}
	}
	h.db.InitTask(ctx, store.TaskRecord{ManifestVersion: 1, TaskID: wakeTaskID, TaskType: "TEST", State: "STARTING", TaskWorkflowID: "task-wf", Data: map[string]any{}})
	if _, err := h.db.ClaimStep(ctx, wakeTaskID, store.StepClaim{
		StepID: stepA, Seq: 4, ActiveTaskTemplateID: stepTemplateID, State: store.StateStartingStep, Data: data(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.WriteRenderState(ctx, wakeTaskID, stepA, 4, "PENDING_PAYMENT", data()); err != nil {
		t.Fatal(err)
	}

	pluginReg := plugins.NewRegistry()
	if err := pluginReg.Register("RESUMING", h.plugin); err != nil {
		t.Fatal(err)
	}
	extReg := extensions.NewRegistry()
	extReg.Register("gate", &mockExtension{
		executeFunc: func(context.Context, *store.TaskRecord, map[string]any, json.RawMessage) error {
			return h.gateErr
		},
	})
	wf := &mockTemporalManager{taskDoneFunc: func(_ context.Context, _, _, _ string, out map[string]any) error {
		h.sent = out
		h.completions++
		return nil
	}}
	h.tm = NewTaskManager(h.db, resumeArtifacts(t), pluginReg, extReg, wf, noopCallback, noopRenderer{})
	return h
}

func TestCompleteTaskStep_ResumerPayloadCompletesTheStep(t *testing.T) {
	h := newResumeHarness(t, "resuming_step")
	var gotConfig string
	var gotNamespace string
	var gotPayload map[string]any
	h.plugin.resume = func(ctx plugins.PluginContext, config json.RawMessage, payload map[string]any) (map[string]any, error) {
		gotConfig, gotNamespace, gotPayload = string(config), ctx.OutputNamespace, payload
		stored := ctx.Record.Data["pay"].(map[string]any)
		return map[string]any{"status": "cancelled", "ref": stored["reference_number"]}, nil
	}

	payload := map[string]any{"status": "success", "__command": "cancel"}
	if err := h.tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, payload); err != nil {
		t.Fatal(err)
	}

	if gotConfig != `{"fee":"5000"}` || gotNamespace != "pay" {
		t.Errorf("Resume got config %s and namespace %q, want the step's plugin_properties and output_namespace", gotConfig, gotNamespace)
	}
	if !reflect.DeepEqual(gotPayload, payload) {
		t.Errorf("Resume got payload %v, want the caller's %v", gotPayload, payload)
	}
	wantSent := map[string]any{"status": "cancelled", "ref": "REF-1", "__command": "cancel"}
	if !reflect.DeepEqual(h.sent, wantSent) {
		t.Errorf("the workflow got %v, want Resume's payload with the call's command %v", h.sent, wantSent)
	}
	row := wakeRow(t, h.db)
	wantData := map[string]any{"in": "value", "pay": map[string]any{"status": "cancelled", "ref": "REF-1"}}
	if !reflect.DeepEqual(row.Data, wantData) {
		t.Errorf("persisted Data = %v, want Resume's payload under the namespace %v", row.Data, wantData)
	}
	if row.State != store.StateAdvancing || row.Seq != 5 {
		t.Errorf("row = (%q, %d), want (ADVANCING, 5)", row.State, row.Seq)
	}
}

func TestCompleteTaskStep_ResumerCannotChangeTheCommand(t *testing.T) {
	tests := map[string]struct {
		payload  map[string]any
		returned map[string]any
		want     map[string]any
	}{
		"a different command is replaced by the call's": {
			payload:  map[string]any{"__command": "cancel"},
			returned: map[string]any{"status": "cancelled", "__command": "submit"},
			want:     map[string]any{"status": "cancelled", "__command": "cancel"},
		},
		"a command added to a call without one is removed": {
			payload:  map[string]any{"status": "success"},
			returned: map[string]any{"status": "success", "__command": "submit"},
			want:     map[string]any{"status": "success"},
		},
		"a nil payload completes with the call's command only": {
			payload:  map[string]any{"__command": "cancel"},
			returned: nil,
			want:     map[string]any{"__command": "cancel"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			h := newResumeHarness(t, "resuming_step")
			h.plugin.resume = func(plugins.PluginContext, json.RawMessage, map[string]any) (map[string]any, error) {
				return tc.returned, nil
			}

			if err := h.tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, tc.payload); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(h.sent, tc.want) {
				t.Errorf("the workflow got %v, want %v", h.sent, tc.want)
			}
		})
	}
}

func TestCompleteTaskStep_ResumerErrorCompletesNothing(t *testing.T) {
	h := newResumeHarness(t, "resuming_step")
	errSettled := errors.New("checkout already settled")
	h.plugin.resume = func(plugins.PluginContext, json.RawMessage, map[string]any) (map[string]any, error) {
		return nil, fmt.Errorf("cancel checkout REF-1: %w", errSettled)
	}

	err := h.tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"__command": "cancel"})

	if !errors.Is(err, errSettled) {
		t.Fatalf("err = %v, want it to wrap the plugin's error", err)
	}
	if h.completions != 0 {
		t.Error("a failed Resume must not complete the step")
	}
	if row := wakeRow(t, h.db); row.State != "PENDING_PAYMENT" || row.Seq != 4 {
		t.Errorf("row = (%q, %d), want it unchanged (PENDING_PAYMENT, 4)", row.State, row.Seq)
	}
}

func TestCompleteTaskStep_RejectedCallNeverReachesResumer(t *testing.T) {
	h := newResumeHarness(t, "resuming_step")
	errForbidden := errors.New("forbidden")
	h.gateErr = errForbidden
	h.plugin.resume = func(plugins.PluginContext, json.RawMessage, map[string]any) (map[string]any, error) {
		return map[string]any{}, nil
	}

	err := h.tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"__command": "cancel"})

	if !errors.Is(err, errForbidden) {
		t.Fatalf("err = %v, want the extension's rejection", err)
	}
	if h.plugin.calls != 0 || h.completions != 0 {
		t.Errorf("Resume ran %d times and the step completed %d times, want neither after a rejection", h.plugin.calls, h.completions)
	}
}

func TestCompleteTaskStep_ResumerGetsCopies(t *testing.T) {
	h := newResumeHarness(t, "resuming_step")
	h.plugin.resume = func(ctx plugins.PluginContext, _ json.RawMessage, payload map[string]any) (map[string]any, error) {
		ctx.Record.Data["in"] = "tampered"
		ctx.Record.State = "TAMPERED"
		payload["status"] = "tampered"
		return map[string]any{"status": "cancelled"}, nil
	}

	payload := map[string]any{"status": "success", "__command": "cancel"}
	if err := h.tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, payload); err != nil {
		t.Fatal(err)
	}

	if payload["status"] != "success" {
		t.Errorf("the caller's payload was changed to %v", payload)
	}
	row := wakeRow(t, h.db)
	if row.Data["in"] != "value" || row.State != store.StateAdvancing {
		t.Errorf("row = (%v, %q), want Resume's changes to its record copy to go nowhere", row.Data, row.State)
	}
}

func TestSaveDraft_DoesNotCallResumer(t *testing.T) {
	h := newResumeHarness(t, "resuming_step")
	h.plugin.resume = func(plugins.PluginContext, json.RawMessage, map[string]any) (map[string]any, error) {
		return map[string]any{}, nil
	}

	if err := h.tm.SaveDraft(context.Background(), wakeTaskID, stepA, map[string]any{"note": "later"}); err != nil {
		t.Fatal(err)
	}

	if h.plugin.calls != 0 {
		t.Errorf("SaveDraft called Resume %d times, want 0", h.plugin.calls)
	}
}

// A step whose plugin is no longer registered can't be resumed: skipping the plugin would let the
// caller's payload through on a step whose plugin may have needed to replace it.
func TestCompleteTaskStep_UnregisteredPluginCompletesNothing(t *testing.T) {
	h := newResumeHarness(t, "orphan_step")

	err := h.tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"__command": "submit"})

	if err == nil {
		t.Fatal("err = nil, want an error for a step whose plugin is not registered")
	}
	if h.completions != 0 {
		t.Error("the step must not complete without its plugin")
	}
}
