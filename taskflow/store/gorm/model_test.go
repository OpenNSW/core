// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package gorm

import (
	"reflect"
	"testing"
	"time"

	"github.com/OpenNSW/core/taskflow/store"
)

// TestRoundTrip_PreservesFields verifies that a domain TaskRecord survives a
// FromDomain -> ToDomain round trip unchanged. CaseID is set once at task
// creation (in the orchestrator), and the GORM mappers must persist and read
// it back verbatim. FromDomain/ToDomain are deliberately pure pass-throughs —
// neither derives CaseID — so this test also guards against derivation logic
// in FromDomain.
//
// CreatedAt/UpdatedAt are intentionally excluded: GORM owns them via
// autoCreateTime/autoUpdateTime, so FromDomain never copies them.
func TestRoundTrip_PreservesFields(t *testing.T) {
	claimedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	original := store.TaskRecord{
		TaskID:               "task-1",
		TaskType:             "TEST",
		State:                "PENDING_USER",
		TaskTemplateID:       "test:task",
		ParentWorkflowID:     "consignment-9--node-3--branch-1",
		ParentStepID:         "step-3",
		CaseID:               "consignment-9",
		ManifestVersion:      3,
		TaskWorkflowID:       "task-wf-1",
		ActiveTaskTemplateID: "tmpl-1",
		ActiveStepID:         "0a0a0a0a-0000-4000-8000-00000000000a",
		Seq:                  4,
		Data:                 map[string]any{"userform": map[string]any{"name": "Alice"}},
		ClaimedBy:            "user-1",
		ClaimedAt:            &claimedAt,
	}

	got := FromDomain(original).ToDomain()

	if got.CaseID != original.CaseID {
		t.Errorf("CaseID not preserved: got %q, want %q", got.CaseID, original.CaseID)
	}
	if got.ManifestVersion != original.ManifestVersion {
		t.Errorf("ManifestVersion: got %d, want %d", got.ManifestVersion, original.ManifestVersion)
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
	if got.ActiveStepID != original.ActiveStepID || got.Seq != original.Seq {
		t.Errorf("step columns: got (%q, %d), want (%q, %d)", got.ActiveStepID, got.Seq, original.ActiveStepID, original.Seq)
	}
	if got.ActiveTaskTemplateID != original.ActiveTaskTemplateID {
		t.Errorf("ActiveTaskTemplateID: got %q, want %q", got.ActiveTaskTemplateID, original.ActiveTaskTemplateID)
	}
	if got.TaskTemplateID != original.TaskTemplateID {
		t.Errorf("TaskTemplateID: got %q, want %q", got.TaskTemplateID, original.TaskTemplateID)
	}
	if !reflect.DeepEqual(got.Data, original.Data) {
		t.Errorf("Data not preserved: got %#v, want %#v", got.Data, original.Data)
	}
	if got.ClaimedBy != original.ClaimedBy || got.ClaimedAt == nil || !got.ClaimedAt.Equal(claimedAt) {
		t.Errorf("claim columns: got (%q, %v), want (%q, %v)", got.ClaimedBy, got.ClaimedAt, original.ClaimedBy, claimedAt)
	}
}

// An unclaimed task must reach the claim columns as NULL, not as "", so "IS NULL" matches it.
func TestFromDomain_UnclaimedIsNull(t *testing.T) {
	m := FromDomain(store.TaskRecord{TaskID: "task-1"})
	if m.ClaimedBy != nil || m.ClaimedAt != nil {
		t.Errorf("claim columns = (%v, %v), want (nil, nil)", m.ClaimedBy, m.ClaimedAt)
	}
	if got := m.ToDomain(); got.ClaimedBy != "" || got.ClaimedAt != nil {
		t.Errorf("round trip claim = (%q, %v), want empty", got.ClaimedBy, got.ClaimedAt)
	}
}

// TestFromDomain_DoesNotDeriveCaseID locks in that FromDomain is a pure
// pass-through for CaseID: the orchestrator sets it, so FromDomain must
// persist exactly what the caller set — even when it diverges from
// ParentWorkflowID's prefix.
func TestFromDomain_DoesNotDeriveCaseID(t *testing.T) {
	r := store.TaskRecord{
		TaskID:           "task-2",
		ParentWorkflowID: "some-other-wf--node--branch",
		CaseID:           "explicit-case",
	}

	if got := FromDomain(r).CaseID; got != "explicit-case" {
		t.Errorf("FromDomain re-derived CaseID: got %q, want %q", got, "explicit-case")
	}
}

// An empty ActiveStepID must reach the uuid column as NULL, not as "", which Postgres rejects.
func TestFromDomain_EmptyActiveStepIDIsNull(t *testing.T) {
	m := FromDomain(store.TaskRecord{TaskID: "task-1"})
	if m.ActiveStepID != nil {
		t.Errorf("ActiveStepID = %q, want nil", *m.ActiveStepID)
	}
	if got := m.ToDomain().ActiveStepID; got != "" {
		t.Errorf("round trip ActiveStepID = %q, want empty", got)
	}
}
