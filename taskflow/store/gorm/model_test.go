// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package gorm

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/OpenNSW/core/taskflow/store"
)

// TestRoundTrip_PreservesFields verifies that a domain TaskRecord survives a
// FromDomain -> ToDomain round trip unchanged. RootWorkflowID is the field that
// regressed: it is derived once at task creation (in the orchestrator) and the
// GORM mappers must persist and read it back verbatim. FromDomain/ToDomain are
// deliberately pure pass-throughs — neither re-derives RootWorkflowID — so this
// test also guards against re-introducing derivation logic in FromDomain.
//
// CreatedAt/UpdatedAt are intentionally excluded: GORM owns them via
// autoCreateTime/autoUpdateTime, so FromDomain never copies them.
func TestRoundTrip_PreservesFields(t *testing.T) {
	original := store.TaskRecord{
		TaskID:               "task-1",
		TaskType:             "TEST",
		State:                "PENDING_USER",
		RenderConfig:         json.RawMessage(`{"layout":"form"}`),
		ParentWorkflowID:     "consignment-9--node-3--branch-1",
		ParentRunID:          "run-1",
		ParentNodeID:         "node-3",
		RootWorkflowID:       "consignment-9",
		TaskWorkflowID:       "task-wf-1",
		TaskRunID:            "task-run-1",
		SubTaskNodeID:        "subtask-node-1",
		ActiveTaskTemplateID: "tmpl-1",
		Data:                 map[string]any{"userform": map[string]any{"name": "Alice"}},
	}

	got := FromDomain(original).ToDomain()

	if got.RootWorkflowID != original.RootWorkflowID {
		t.Errorf("RootWorkflowID not preserved: got %q, want %q", got.RootWorkflowID, original.RootWorkflowID)
	}
	if got.TaskID != original.TaskID {
		t.Errorf("TaskID: got %q, want %q", got.TaskID, original.TaskID)
	}
	if got.ParentWorkflowID != original.ParentWorkflowID {
		t.Errorf("ParentWorkflowID: got %q, want %q", got.ParentWorkflowID, original.ParentWorkflowID)
	}
	if got.TaskWorkflowID != original.TaskWorkflowID {
		t.Errorf("TaskWorkflowID: got %q, want %q", got.TaskWorkflowID, original.TaskWorkflowID)
	}
	if got.ActiveTaskTemplateID != original.ActiveTaskTemplateID {
		t.Errorf("ActiveTaskTemplateID: got %q, want %q", got.ActiveTaskTemplateID, original.ActiveTaskTemplateID)
	}
	if string(got.RenderConfig) != string(original.RenderConfig) {
		t.Errorf("RenderConfig: got %s, want %s", got.RenderConfig, original.RenderConfig)
	}
	if !reflect.DeepEqual(got.Data, original.Data) {
		t.Errorf("Data not preserved: got %#v, want %#v", got.Data, original.Data)
	}
}

// TestFromDomain_DoesNotDeriveRootWorkflowID locks in that FromDomain is a pure
// pass-through for RootWorkflowID. A previous iteration derived it here from
// ParentWorkflowID; the source of truth now lives in the orchestrator, so
// FromDomain must persist exactly what the caller set — even when it diverges
// from ParentWorkflowID's prefix.
func TestFromDomain_DoesNotDeriveRootWorkflowID(t *testing.T) {
	r := store.TaskRecord{
		TaskID:           "task-2",
		ParentWorkflowID: "some-other-wf--node--branch",
		RootWorkflowID:   "explicit-root",
	}

	if got := FromDomain(r).RootWorkflowID; got != "explicit-root" {
		t.Errorf("FromDomain re-derived RootWorkflowID: got %q, want %q", got, "explicit-root")
	}
}
