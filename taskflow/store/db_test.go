package store

import (
	"context"
	"testing"
	"time"
)

// testStore implements store.TaskStore to make sure the interface methods compile and work correctly
type testStore struct {
	tasks map[string]TaskRecord
}

func (t *testStore) SaveTask(_ context.Context, record TaskRecord) {
	t.tasks[record.TaskID] = record
}

func (t *testStore) GetTask(_ context.Context, taskID string) (TaskRecord, bool) {
	record, ok := t.tasks[taskID]
	return record, ok
}

func (t *testStore) GetTaskByWorkflowID(_ context.Context, workflowID string) (TaskRecord, bool) {
	for _, record := range t.tasks {
		if record.TaskWorkflowID == workflowID {
			return record, true
		}
	}
	return TaskRecord{}, false
}

func (t *testStore) GetAllTasks(_ context.Context, parentWorkflowID string) []TaskRecord {
	var list []TaskRecord
	for _, record := range t.tasks {
		if parentWorkflowID != "" && record.ParentWorkflowID != parentWorkflowID {
			continue
		}
		list = append(list, record)
	}
	return list
}

func TestTaskStoreInterface(t *testing.T) {
	var store TaskStore = &testStore{tasks: make(map[string]TaskRecord)}

	record := TaskRecord{
		TaskID:           "test-1",
		TaskType:         "TEST",
		State:            "PENDING_USER",
		ParentWorkflowID: "parent-wf-1",
		ParentRunID:      "parent-run-1",
		ParentNodeID:     "node-1",
		TaskWorkflowID:   "task-wf-1",
		TaskRunID:        "task-run-1",
		SubTaskNodeID:    "activity-1",
		Data:             map[string]any{"userform": map[string]any{"name": "Alice"}},
		CreatedAt:        time.Now(),
	}

	ctx := context.Background()
	store.SaveTask(ctx, record)

	fetched, ok := store.GetTask(ctx, "test-1")
	if !ok {
		t.Fatal("Expected task to be fetched")
	}

	if fetched.TaskType != "TEST" {
		t.Errorf("Expected TaskType 'TEST', got %s", fetched.TaskType)
	}

	fetchedTaskWF, ok := store.GetTaskByWorkflowID(ctx, "task-wf-1")
	if !ok {
		t.Fatal("Expected task to be fetched by Task workflow ID")
	}
	if fetchedTaskWF.TaskID != "test-1" {
		t.Errorf("Expected TaskID 'test-1', got %s", fetchedTaskWF.TaskID)
	}

	all := store.GetAllTasks(ctx, "")
	if len(all) != 1 {
		t.Errorf("Expected exactly 1 task record, got %d", len(all))
	}
}
