// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// testStore implements store.TaskStore to make sure the interface methods compile and work correctly
type testStore struct {
	tasks map[string]TaskRecord
}

func (t *testStore) InitTask(_ context.Context, record TaskRecord) error {
	t.tasks[record.TaskID] = record
	return nil
}

func (t *testStore) GetTask(_ context.Context, taskID string) (TaskRecord, error) {
	record, ok := t.tasks[taskID]
	if !ok {
		return TaskRecord{}, ErrTaskNotFound
	}
	return record, nil
}

func (t *testStore) GetTaskByWorkflowID(_ context.Context, workflowID string) (TaskRecord, error) {
	for _, record := range t.tasks {
		if record.TaskWorkflowID == workflowID {
			return record, nil
		}
	}
	return TaskRecord{}, ErrTaskNotFound
}

// GetTasks applies the filter's reference rule, TaskFilter.Matches, to the stored tasks.
func (t *testStore) GetTasks(_ context.Context, filter TaskFilter) ([]TaskRecord, error) {
	var list []TaskRecord
	for _, record := range t.tasks {
		if filter.Matches(record) {
			list = append(list, record)
		}
	}
	return list, nil
}

func (t *testStore) FindTask(ctx context.Context, filter TaskFilter) (TaskRef, error) {
	if filter.CaseID == "" {
		return TaskRef{}, ErrCaseIDRequired
	}
	list, _ := t.GetTasks(ctx, filter)
	if len(list) == 0 {
		return TaskRef{}, ErrTaskNotFound
	}
	return TaskRef{TaskID: list[0].TaskID, StepID: list[0].ActiveStepID}, nil
}

func (t *testStore) ClaimStep(context.Context, string, StepClaim) (int64, error) { return 0, nil }

func (t *testStore) WriteRenderState(context.Context, string, string, int64, string, map[string]any) (int64, error) {
	return 0, nil
}

func (t *testStore) PersistSubmission(context.Context, string, string, int64, map[string]any) (int64, error) {
	return 0, nil
}

func (t *testStore) WriteDraft(context.Context, string, string, int64, map[string]any) (int64, error) {
	return 0, nil
}

func (t *testStore) CompleteTask(context.Context, string, int64) (int64, error) { return 0, nil }

func (t *testStore) CancelTask(context.Context, string) (int64, error) { return 0, nil }

func (t *testStore) HoldTask(context.Context, string, int64) (int64, error) { return 0, nil }

func (t *testStore) ClaimTask(context.Context, string, string, time.Time) (int64, error) {
	return 0, nil
}

func (t *testStore) ReleaseTask(context.Context, string, string) (int64, error) { return 0, nil }

// The copy must not share ClaimedAt with the original, as it shares no other reference state.
func TestDeepCopy_CopiesClaimedAt(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	original := TaskRecord{TaskID: "test-1", ClaimedBy: "user-1", ClaimedAt: &at}

	cp := original.DeepCopy()
	if cp.ClaimedBy != "user-1" || cp.ClaimedAt == nil || !cp.ClaimedAt.Equal(at) {
		t.Fatalf("claim not copied: got (%q, %v)", cp.ClaimedBy, cp.ClaimedAt)
	}
	if cp.ClaimedAt == original.ClaimedAt {
		t.Fatal("ClaimedAt is shared with the original")
	}

	if got := (TaskRecord{TaskID: "test-2"}).DeepCopy().ClaimedAt; got != nil {
		t.Errorf("unclaimed copy ClaimedAt = %v, want nil", got)
	}
}

func TestTaskStoreInterface(t *testing.T) {
	var store TaskStore = &testStore{tasks: make(map[string]TaskRecord)}

	record := TaskRecord{
		TaskID:           "test-1",
		TaskType:         "TEST",
		State:            "PENDING_USER",
		ParentWorkflowID: "parent-wf-1",
		ParentStepID:     "step-1",
		TaskWorkflowID:   "task-wf-1",
		ReadRoles:        []string{"trader"},
		Data:             map[string]any{"userform": map[string]any{"name": "Alice"}},
		CreatedAt:        time.Now(),
	}

	ctx := context.Background()
	store.InitTask(ctx, record)

	fetched, err := store.GetTask(ctx, "test-1")
	if err != nil {
		t.Fatalf("Expected task to be fetched: %v", err)
	}

	if fetched.TaskType != "TEST" {
		t.Errorf("Expected TaskType 'TEST', got %s", fetched.TaskType)
	}

	fetchedTaskWF, err := store.GetTaskByWorkflowID(ctx, "task-wf-1")
	if err != nil {
		t.Fatalf("Expected task to be fetched by Task workflow ID: %v", err)
	}
	if _, err := store.GetTask(ctx, "missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("GetTask(missing) err = %v, want ErrTaskNotFound", err)
	}
	if fetchedTaskWF.TaskID != "test-1" {
		t.Errorf("Expected TaskID 'test-1', got %s", fetchedTaskWF.TaskID)
	}

	all, err := store.GetTasks(ctx, TaskFilter{Roles: []string{"trader"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("Expected exactly 1 task record, got %d", len(all))
	}
}

// Each new TaskFilter field narrows Matches only when set.
func TestTaskFilterMatchesStepFields(t *testing.T) {
	r := TaskRecord{CaseID: "C1", ParentWorkflowID: "C1:export", ActiveTaskTemplateID: "await", State: "QUEUED_EXTERNALLY"}
	for _, tc := range []struct {
		filter TaskFilter
		want   bool
	}{
		{TaskFilter{}, true},
		{TaskFilter{CaseID: "C1", ParentWorkflowID: "C1:export", ActiveTemplateID: "await", State: "QUEUED_EXTERNALLY"}, true},
		{TaskFilter{ParentWorkflowID: "C1:payment"}, false},
		{TaskFilter{ActiveTemplateID: "form"}, false},
		{TaskFilter{State: "PENDING_USER"}, false},
	} {
		if got := tc.filter.Matches(r); got != tc.want {
			t.Errorf("%+v.Matches = %v, want %v", tc.filter, got, tc.want)
		}
	}
}
