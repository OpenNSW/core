// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"reflect"
	"testing"

	"github.com/OpenNSW/core/artifact/adapter/types"
	"github.com/OpenNSW/core/taskflow/store"
)

func TestCompletionResult(t *testing.T) {
	own := map[string]any{"reference_number": "TNSW-1"}
	withOutput := store.TaskRecord{Data: map[string]any{"in": "value", "payment": own}}
	namespaced := types.StepTemplate{OutputNamespace: "payment"}

	cases := []struct {
		name    string
		record  store.TaskRecord
		step    types.StepTemplate
		payload map[string]any
		want    map[string]any
	}{
		{
			name:    "the step's own output is added beside the submission",
			record:  withOutput,
			step:    namespaced,
			payload: map[string]any{"__command": "change_method"},
			want:    map[string]any{"__command": "change_method", "payment": own},
		},
		{
			name:    "a submitted key of the namespace's name is replaced by the stored output",
			record:  withOutput,
			step:    namespaced,
			payload: map[string]any{"payment": map[string]any{"reference_number": "FORGED"}},
			want:    map[string]any{"payment": own},
		},
		{
			name:    "an empty payload still carries the output",
			record:  withOutput,
			step:    namespaced,
			payload: nil,
			want:    map[string]any{"payment": own},
		},
		{
			name:    "nothing stored under the namespace leaves the payload as it is",
			record:  store.TaskRecord{Data: map[string]any{"in": "value"}},
			step:    namespaced,
			payload: map[string]any{"name": "Alice"},
			want:    map[string]any{"name": "Alice"},
		},
		{
			name:    "a step without a namespace leaves the payload as it is",
			record:  withOutput,
			step:    types.StepTemplate{},
			payload: map[string]any{"name": "Alice"},
			want:    map[string]any{"name": "Alice"},
		},
		{
			name:    "a step that takes drafts leaves the payload as it is",
			record:  withOutput,
			step:    types.StepTemplate{OutputNamespace: "payment", Draft: &types.DraftConfig{Command: "save_as_draft"}},
			payload: map[string]any{"name": "Alice"},
			want:    map[string]any{"name": "Alice"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := completionResult(c.record, c.step, c.payload)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("completionResult = %v, want %v", got, c.want)
			}
		})
	}
}

// The caller's payload and the stored data are read, never written: the payload is also what the
// extensions and the persisted submission are built from.
func TestCompletionResult_DoesNotChangeItsInputs(t *testing.T) {
	record := store.TaskRecord{Data: map[string]any{"payment": map[string]any{"reference_number": "TNSW-1"}}}
	payload := map[string]any{"__command": "change_method"}

	got := completionResult(record, types.StepTemplate{OutputNamespace: "payment"}, payload)

	if !reflect.DeepEqual(payload, map[string]any{"__command": "change_method"}) {
		t.Errorf("payload = %v, want it unchanged", payload)
	}
	if _, ok := got["payment"]; !ok {
		t.Fatalf("result = %v, want the step's output", got)
	}
	got["extra"] = true
	if _, ok := payload["extra"]; ok {
		t.Error("the result shares its map with the payload")
	}
}

// What a step stored when it started reaches the workflow when the step completes, and a caller
// cannot replace it with a value of its own.
func TestCompleteTaskStep_WorkflowGetsTheStepsOwnOutput(t *testing.T) {
	db := newSafeMockTaskStore()
	tm, wf := wakeUpFixture(t, db)
	if _, err := db.WriteRenderState(context.Background(), wakeTaskID, stepA, 4, "PENDING_USER", map[string]any{
		"in":       "value",
		"userform": map[string]any{"reference_number": "TNSW-1"},
	}); err != nil {
		t.Fatal(err)
	}
	var gotOut map[string]any
	wf.taskDoneFunc = func(_ context.Context, _, _, _ string, out map[string]any) error {
		gotOut = out
		return nil
	}

	payload := map[string]any{
		"__command": "change_method",
		"userform":  map[string]any{"reference_number": "FORGED"},
	}
	if err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, payload); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		"__command": "change_method",
		"userform":  map[string]any{"reference_number": "TNSW-1"},
	}
	if !reflect.DeepEqual(gotOut, want) {
		t.Errorf("the workflow got %v, want %v", gotOut, want)
	}
	if got := payload["userform"].(map[string]any)["reference_number"]; got != "FORGED" {
		t.Errorf("the caller's payload was changed: reference_number = %v", got)
	}
}
