// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
	"github.com/OpenNSW/core/internal/maputil"
	"github.com/OpenNSW/core/taskflow/extensions"
	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/renderer"
	"github.com/OpenNSW/core/taskflow/store"
	engine "github.com/OpenNSW/core/workflow"
	"go.temporal.io/sdk/activity"
)

// Tests are run against the real artifact.Registry populated with in-memory mocks

// ---------------------------------------------------------------------------
// No-op renderer
// ---------------------------------------------------------------------------

type noopRenderer struct{}

func (noopRenderer) Render(_ context.Context, _ json.RawMessage, _ renderer.Facts) (json.RawMessage, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type mockTemporalManager struct {
	startWorkflowFunc func(ctx context.Context, workflowID string, def engine.WorkflowDefinition, initialVars map[string]any) error
	taskDoneFunc      func(ctx context.Context, workflowID string, runID string, activityID string, result map[string]any) error
	startWorkerFunc   func() error
	stopWorkerFunc    func()
}

func (m *mockTemporalManager) StartWorkflow(ctx context.Context, workflowID string, def engine.WorkflowDefinition, initialVars map[string]any) error {
	if m.startWorkflowFunc != nil {
		return m.startWorkflowFunc(ctx, workflowID, def, initialVars)
	}
	return nil
}

func (m *mockTemporalManager) TaskDone(ctx context.Context, workflowID string, runID string, activityID string, result map[string]any) error {
	if m.taskDoneFunc != nil {
		return m.taskDoneFunc(ctx, workflowID, runID, activityID, result)
	}
	return nil
}

func (m *mockTemporalManager) StartWorker() error {
	if m.startWorkerFunc != nil {
		return m.startWorkerFunc()
	}
	return nil
}

func (m *mockTemporalManager) StopWorker() {
	if m.stopWorkerFunc != nil {
		m.stopWorkerFunc()
	}
}

func (m *mockTemporalManager) TaskUpdate(ctx context.Context, workflowID string, runID string, event engine.UpdateEvent) error {
	return nil
}

func (m *mockTemporalManager) GetStatus(ctx context.Context, workflowID string) (*engine.WorkflowInstance, error) {
	return nil, nil
}

func (m *mockTemporalManager) RegisterDefinitionHandler(_ func(templateID string) (engine.WorkflowDefinition, error)) {
}

type safeMockTaskStore struct {
	mu    sync.RWMutex
	tasks map[string]store.TaskRecord
}

func newSafeMockTaskStore() *safeMockTaskStore {
	return &safeMockTaskStore{
		tasks: make(map[string]store.TaskRecord),
	}
}

func (s *safeMockTaskStore) SaveTask(_ context.Context, task store.TaskRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.TaskID] = task
}

func (s *safeMockTaskStore) GetTask(_ context.Context, taskID string) (store.TaskRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, exists := s.tasks[taskID]
	return task, exists
}

func (s *safeMockTaskStore) GetTaskByWorkflowID(_ context.Context, workflowID string) (store.TaskRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tasks {
		if t.TaskWorkflowID == workflowID {
			return t, true
		}
	}
	return store.TaskRecord{}, false
}

func (s *safeMockTaskStore) GetAllTasks(_ context.Context, parentWorkflowID string) []store.TaskRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]store.TaskRecord, 0, len(s.tasks))
	for _, r := range s.tasks {
		if parentWorkflowID != "" && r.ParentWorkflowID != parentWorkflowID {
			continue
		}
		out = append(out, r)
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestPluginsRegistry() *plugins.Registry {
	pr := plugins.NewRegistry()
	pr.Register("USER_INPUT", plugins.NewUserInputPlugin())

	// Use an offline-friendly mock HTTP dispatcher for unit tests to avoid real HTTP requests
	mockDispatcher := func(ctx context.Context, url string, taskID string, payload map[string]any) error {
		return nil
	}
	pr.Register("EXTERNAL_REVIEW", plugins.NewExternalReviewPlugin(mockDispatcher))
	return pr
}

func newTestTaskManager(db store.TaskStore, registry *artifact.Registry, tm engine.TemporalManager, cb TaskCompletedCallback) *TaskManager {
	return NewTaskManager(db, registry, newTestPluginsRegistry(), nil, tm, cb, noopRenderer{})
}

func noopCallback(_, _, _ string, _ map[string]any) error { return nil }

func newTestRegistry() *artifact.Registry {
	reg := artifact.NewRegistry()
	m := testutil.MemLoader{
		"task_test_template.json": []byte(`{
			"id": "test_template",
			"type": "TEST",
			"workflow_id": "test_workflow_v1",
			"render_config_id": "test_render_config"
		}`),
		"wf_test_workflow_v1.json": []byte(`{
			"id": "test_workflow_v1",
			"name": "Test Workflow",
			"version": 1,
			"nodes": []
		}`),
		"generic_render_config.json": []byte(`{}`),
		"subtask_generic_user_input.json": []byte(`{
			"id": "generic_user_input",
			"task_type": "USER_INPUT",
			"output_namespace": "userform",
			"plugin_properties": {}
		}`),
		"subtask_generic_user_input_with_extensions.json": []byte(`{
			"id": "generic_user_input_with_extensions",
			"task_type": "USER_INPUT",
			"output_namespace": "form",
			"plugin_properties": {},
			"extensions": [
				{"id": "validator", "phase": "PRE_RESUME"},
				{"id": "logger", "phase": "POST_RESUME"}
			]
		}`),
		"subtask_generic_external_review.json": []byte(`{
			"id": "generic_external_review",
			"task_type": "EXTERNAL_REVIEW",
			"output_namespace": "reviewerform",
			"plugin_properties": {
				"external_url": "http://example.com"
			}
		}`),
	}
	reg.RegisterLoader("mem", m)
	reg.RegisterArtifact("test_template", "task_template", "", "mem", "task_test_template.json")
	reg.RegisterArtifact("test_workflow_v1", "workflow", "", "mem", "wf_test_workflow_v1.json")
	reg.RegisterArtifact("test_render_config", "generic_template", "", "mem", "generic_render_config.json")
	reg.RegisterArtifact("generic_user_input", "subtask_template", "", "mem", "subtask_generic_user_input.json")
	reg.RegisterArtifact("generic_user_input_with_extensions", "subtask_template", "", "mem", "subtask_generic_user_input_with_extensions.json")
	reg.RegisterArtifact("generic_external_review", "subtask_template", "", "mem", "subtask_generic_external_review.json")
	return reg
}

// ---------------------------------------------------------------------------
// Lifecycle integration test
// ---------------------------------------------------------------------------

func TestTaskManager_Lifecycle(t *testing.T) {
	storeMock := newSafeMockTaskStore()
	registry := newTestRegistry()

	taskWorkflowCalled := false
	mockTaskWFManager := &mockTemporalManager{
		startWorkflowFunc: func(ctx context.Context, workflowID string, def engine.WorkflowDefinition, initialVars map[string]any) error {
			taskWorkflowCalled = true
			if initialVars["_task_id"] == "" {
				return errors.New("missing task ID in initialVars")
			}
			return nil
		},
	}

	parentCallbackCalled := false
	onCompleted := func(parentWorkflowID string, parentRunID string, parentNodeID string, finalVars map[string]any) error {
		parentCallbackCalled = true
		return nil
	}

	tm := newTestTaskManager(storeMock, registry, mockTaskWFManager, onCompleted)

	// 1. StartTask
	payload := engine.TaskPayload{
		WorkflowID:     "parent-workflow",
		RunID:          "parent-run",
		NodeID:         "node-1",
		TaskTemplateID: "test_template",
		Inputs:         map[string]any{"userform.name": "Alice"},
	}

	if _, err := tm.StartTask(context.Background(), payload); err != nil && !errors.Is(err, activity.ErrResultPending) {
		t.Fatalf("StartTask failed: %v", err)
	}
	if !taskWorkflowCalled {
		t.Error("expected task sub-workflow to be started")
	}

	tasks := storeMock.GetAllTasks(context.Background(), "")
	if len(tasks) != 1 {
		t.Fatalf("expected exactly 1 task record, got %d", len(tasks))
	}
	task := tasks[0]
	if task.State != "STARTING" {
		t.Errorf("expected status 'STARTING', got '%s'", task.State)
	}
	if task.ParentWorkflowID != "parent-workflow" {
		t.Errorf("expected parent workflow 'parent-workflow', got '%s'", task.ParentWorkflowID)
	}
	// A top-level workflow ID has no "--" separator, so the root consignment ID
	// is the workflow ID itself.
	if task.RootWorkflowID != "parent-workflow" {
		t.Errorf("expected root workflow 'parent-workflow', got '%s'", task.RootWorkflowID)
	}

	// 2. StartSubTask — generic_user_input
	payloadTaskWF := engine.TaskPayload{
		WorkflowID:     task.TaskWorkflowID,
		RunID:          "task-run",
		NodeID:         "task-node",
		TaskTemplateID: "generic_user_input",
	}
	if _, err := tm.StartSubTask(context.Background(), payloadTaskWF); err != nil && !errors.Is(err, activity.ErrResultPending) {
		t.Fatalf("StartSubTask failed: %v", err)
	}

	task, _ = storeMock.GetTask(context.Background(), task.TaskID)
	if task.State != "PENDING_USER" {
		t.Errorf("expected status 'PENDING_USER', got '%s'", task.State)
	}
	if task.TaskRunID != "task-run" {
		t.Errorf("expected Task run ID 'task-run', got '%s'", task.TaskRunID)
	}

	// 3. CompleteTaskStep
	taskDoneCalled := false
	mockTaskWFManager.taskDoneFunc = func(ctx context.Context, workflowID string, runID string, activityID string, result map[string]any) error {
		taskDoneCalled = true
		if workflowID != task.TaskWorkflowID {
			t.Errorf("expected task workflow ID %s, got %s", task.TaskWorkflowID, workflowID)
		}
		if activityID != "task-node" {
			t.Errorf("expected active activity ID 'task-node', got %s", activityID)
		}
		return nil
	}

	// The caller no longer namespaces the payload — the subtask template's
	// OutputNamespace ("userform") does that on the server side.
	userData := map[string]any{
		"applicant_name": "Alice",
		"email":          "alice@example.com",
	}
	if err := tm.CompleteTaskStep(context.Background(), task.TaskID, userData); err != nil {
		t.Fatalf("CompleteTaskStep failed: %v", err)
	}
	if !taskDoneCalled {
		t.Error("expected TaskDone to be called on task workflow")
	}

	task, _ = storeMock.GetTask(context.Background(), task.TaskID)
	userform, ok := task.Data["userform"].(map[string]any)
	if !ok {
		t.Fatal("expected 'userform' to be a nested map")
	}
	if userform["email"] != "alice@example.com" {
		t.Errorf("expected userform.email 'alice@example.com', got '%v'", userform["email"])
	}

	// 4. HandleTaskCompletion
	finalVars := map[string]any{"reviewerform.review_outcome": "approve"}
	if err := tm.HandleTaskCompletion(context.Background(), task.TaskWorkflowID, finalVars); err != nil {
		t.Fatalf("HandleTaskCompletion failed: %v", err)
	}

	task, _ = storeMock.GetTask(context.Background(), task.TaskID)
	if task.State != "COMPLETED" {
		t.Errorf("expected task status 'COMPLETED', got '%s'", task.State)
	}
	if !parentCallbackCalled {
		t.Error("expected parent wake-up callback to be invoked")
	}
}

// ---------------------------------------------------------------------------
// StartTask — error paths
// ---------------------------------------------------------------------------

func TestStartTask_UnknownTemplateID(t *testing.T) {
	tm := newTestTaskManager(newSafeMockTaskStore(), newTestRegistry(), &mockTemporalManager{}, noopCallback)

	_, err := tm.StartTask(context.Background(), engine.TaskPayload{
		WorkflowID:     "parent-wf",
		TaskTemplateID: "does_not_exist",
	})
	if err == nil {
		t.Fatal("expected error for unknown template ID, got nil")
	}
}

func TestStartTask_TaskWorkflowManagerError(t *testing.T) {
	mockTaskWF := &mockTemporalManager{
		startWorkflowFunc: func(_ context.Context, _ string, _ engine.WorkflowDefinition, _ map[string]any) error {
			return errors.New("temporal unavailable")
		},
	}

	tm := newTestTaskManager(newSafeMockTaskStore(), newTestRegistry(), mockTaskWF, noopCallback)

	_, err := tm.StartTask(context.Background(), engine.TaskPayload{
		WorkflowID:     "parent-wf",
		TaskTemplateID: "test_template",
	})
	if err == nil {
		t.Fatal("expected error when task StartWorkflow fails, got nil")
	}
}

// TestStartTask_DerivesRootWorkflowID verifies the consignment ID extraction:
// for SPLIT_TASK child workflows (format "{root}--{nodeID}--{branchID}") the
// root is the segment before the first "--"; a plain top-level workflow ID is
// its own root.
func TestStartTask_DerivesRootWorkflowID(t *testing.T) {
	cases := []struct {
		name       string
		workflowID string
		wantRoot   string
	}{
		{"top-level workflow", "consignment-123", "consignment-123"},
		{"split-task child", "consignment-123--node-5--branch-2", "consignment-123"},
		{"single separator", "root--child", "root"},
		{"empty workflow id", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newSafeMockTaskStore()
			tm := newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{}, noopCallback)

			payload := engine.TaskPayload{
				WorkflowID:     tc.workflowID,
				RunID:          "run-1",
				NodeID:         "node-1",
				TaskTemplateID: "test_template",
			}
			if _, err := tm.StartTask(context.Background(), payload); err != nil && !errors.Is(err, activity.ErrResultPending) {
				t.Fatalf("StartTask failed: %v", err)
			}

			task, ok := db.GetTask(context.Background(), "node-1")
			if !ok {
				t.Fatal("expected task record to be saved")
			}
			if task.RootWorkflowID != tc.wantRoot {
				t.Errorf("RootWorkflowID = %q, want %q", task.RootWorkflowID, tc.wantRoot)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// StartSubTask — error paths
// ---------------------------------------------------------------------------

func TestStartSubTask_UnknownWorkflowID(t *testing.T) {
	tm := newTestTaskManager(newSafeMockTaskStore(), newTestRegistry(), &mockTemporalManager{}, noopCallback)

	_, err := tm.StartSubTask(context.Background(), engine.TaskPayload{
		WorkflowID:     "workflow-that-was-never-registered",
		TaskTemplateID: "generic_user_input",
	})
	if err == nil {
		t.Fatal("expected error for unknown workflow ID, got nil")
	}
}

func TestStartSubTask_UnknownTaskTemplateID(t *testing.T) {
	db := newSafeMockTaskStore()
	db.SaveTask(context.Background(), store.TaskRecord{
		TaskID:         "task-1",
		TaskWorkflowID: "task-workflow-1",
		State:          "STARTING",
		Data:           map[string]any{},
	})

	tm := newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{}, noopCallback)

	_, err := tm.StartSubTask(context.Background(), engine.TaskPayload{
		WorkflowID:     "task-workflow-1",
		TaskTemplateID: "not_a_real_template",
	})
	if err == nil {
		t.Fatal("expected error for unknown task_template_id in StartSubTask, got nil")
	}
}

func TestStartSubTask_ExternalReviewPath(t *testing.T) {
	db := newSafeMockTaskStore()
	db.SaveTask(context.Background(), store.TaskRecord{
		TaskID:         "task-ext",
		TaskWorkflowID: "task-ext-workflow",
		State:          "STARTING",
		Data:           map[string]any{},
	})

	tm := newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{}, noopCallback)

	_, err := tm.StartSubTask(context.Background(), engine.TaskPayload{
		WorkflowID:     "task-ext-workflow",
		RunID:          "run-1",
		NodeID:         "node-ext",
		TaskTemplateID: "generic_external_review",
	})
	if err != nil && !errors.Is(err, activity.ErrResultPending) {
		t.Fatalf("StartSubTask for generic_external_review failed: %v", err)
	}

	task, _ := db.GetTask(context.Background(), "task-ext")
	if task.State != "QUEUED_EXTERNALLY" {
		t.Errorf("expected status QUEUED_EXTERNALLY, got %s", task.State)
	}
}

// ---------------------------------------------------------------------------
// CompleteTaskStep — error paths
// ---------------------------------------------------------------------------

func TestCompleteTaskStep_UnknownTaskID(t *testing.T) {
	tm := newTestTaskManager(newSafeMockTaskStore(), newTestRegistry(), &mockTemporalManager{}, noopCallback)

	err := tm.CompleteTaskStep(context.Background(), "task-ghost", map[string]any{"x": 1})
	if err == nil {
		t.Fatal("expected error for unknown task ID, got nil")
	}
}

func TestCompleteTaskStep_AlreadyCompleted(t *testing.T) {
	db := newSafeMockTaskStore()
	db.SaveTask(context.Background(), store.TaskRecord{
		TaskID: "task-done",
		State:  "COMPLETED",
		Data:   map[string]any{},
	})

	tm := newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{}, noopCallback)

	err := tm.CompleteTaskStep(context.Background(), "task-done", map[string]any{"x": 1})
	if err == nil {
		t.Fatal("expected error for already-completed task, got nil")
	}
}

func TestCompleteTaskStep_NoActiveSubTask(t *testing.T) {
	db := newSafeMockTaskStore()
	db.SaveTask(context.Background(), store.TaskRecord{
		TaskID: "task-starting",
		State:  "STARTING",
		Data:   map[string]any{},
	})

	tm := newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{}, noopCallback)

	err := tm.CompleteTaskStep(context.Background(), "task-starting", map[string]any{"x": 1})
	if err == nil {
		t.Fatal("expected error for task with no active subtask step, got nil")
	}
}

// ---------------------------------------------------------------------------
// HandleTaskCompletion — edge cases
// ---------------------------------------------------------------------------

func TestHandleTaskCompletion_UnknownWorkflowID_ReturnsNil(t *testing.T) {
	tm := newTestTaskManager(newSafeMockTaskStore(), newTestRegistry(), &mockTemporalManager{}, noopCallback)

	err := tm.HandleTaskCompletion(context.Background(), "unknown-workflow", map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("expected nil for unknown workflow, got: %v", err)
	}
}

func TestHandleTaskCompletion_CallbackError_TaskNotMarkedCompleted(t *testing.T) {
	db := newSafeMockTaskStore()
	db.SaveTask(context.Background(), store.TaskRecord{
		TaskID:         "task-cb-err",
		TaskWorkflowID: "task-cb-workflow",
		State:          "PENDING_USER",
		Data:           map[string]any{},
	})

	callbackErr := errors.New("parent unreachable")
	tm := newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{},
		func(_, _, _ string, _ map[string]any) error { return callbackErr },
	)

	err := tm.HandleTaskCompletion(context.Background(), "task-cb-workflow", map[string]any{})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("expected callback error to be propagated, got: %v", err)
	}

	// Task must NOT be marked COMPLETED when the parent-workflow callback fails:
	// marking it COMPLETED before a successful callback would make the state
	// unrecoverable (CompleteTaskStep rejects re-delivery once State=COMPLETED).
	task, _ := db.GetTask(context.Background(), "task-cb-err")
	if task.State == "COMPLETED" {
		t.Errorf("task must not be marked COMPLETED when callback failed, got %s", task.State)
	}
}

// ---------------------------------------------------------------------------
// setNestedKey — unit tests
// ---------------------------------------------------------------------------

func TestSetNestedKey_SingleLevel(t *testing.T) {
	m := map[string]any{}
	maputil.SetNestedKey(m, "name", "Alice")
	if m["name"] != "Alice" {
		t.Errorf("expected 'Alice', got %v", m["name"])
	}
}

func TestSetNestedKey_TwoLevels(t *testing.T) {
	m := map[string]any{}
	maputil.SetNestedKey(m, "user.email", "alice@example.com")

	sub, ok := m["user"].(map[string]any)
	if !ok {
		t.Fatal("expected 'user' to be a map")
	}
	if sub["email"] != "alice@example.com" {
		t.Errorf("expected 'alice@example.com', got %v", sub["email"])
	}
}

func TestSetNestedKey_ThreeLevels(t *testing.T) {
	m := map[string]any{}
	maputil.SetNestedKey(m, "a.b.c", 42)

	a, ok := m["a"].(map[string]any)
	if !ok {
		t.Fatal("expected 'a' to be a map")
	}
	b, ok := a["b"].(map[string]any)
	if !ok {
		t.Fatal("expected 'a.b' to be a map")
	}
	if b["c"] != 42 {
		t.Errorf("expected 42, got %v", b["c"])
	}
}

func TestSetNestedKey_EmptyPath_IsNoop(t *testing.T) {
	m := map[string]any{"existing": "value"}
	maputil.SetNestedKey(m, "", "should_not_appear")
	if len(m) != 1 {
		t.Errorf("expected map to be unchanged, got %v", m)
	}
}

func TestSetNestedKey_OverwritesNonMapIntermediate(t *testing.T) {
	m := map[string]any{"user": "not-a-map"}
	maputil.SetNestedKey(m, "user.email", "alice@example.com")

	sub, ok := m["user"].(map[string]any)
	if !ok {
		t.Fatal("expected 'user' to be replaced with a map")
	}
	if sub["email"] != "alice@example.com" {
		t.Errorf("expected 'alice@example.com', got %v", sub["email"])
	}
}

func TestSetNestedKey_MergesIntoExistingMap(t *testing.T) {
	m := map[string]any{
		"user": map[string]any{"name": "Bob"},
	}
	maputil.SetNestedKey(m, "user.email", "bob@example.com")

	sub := m["user"].(map[string]any)
	if sub["name"] != "Bob" {
		t.Error("existing key 'name' should not be removed")
	}
	if sub["email"] != "bob@example.com" {
		t.Errorf("expected 'bob@example.com', got %v", sub["email"])
	}
}

// ---------------------------------------------------------------------------
// Extensions Pipeline tests
// ---------------------------------------------------------------------------

type mockExtension struct {
	executeFunc func(ctx context.Context, record *store.TaskRecord, payload map[string]any, properties json.RawMessage) error
}

func (m *mockExtension) Execute(ctx context.Context, record *store.TaskRecord, payload map[string]any, properties json.RawMessage) error {
	if m.executeFunc != nil {
		return m.executeFunc(ctx, record, payload, properties)
	}
	return nil
}

func TestTaskManager_ExtensionsPipeline(t *testing.T) {
	db := newSafeMockTaskStore()
	registry := newTestRegistry()
	extReg := extensions.NewRegistry()

	preExecuted := false
	postExecuted := false
	var postExecutedWg sync.WaitGroup
	postExecutedWg.Add(1)

	// Register a validator extension (wired to the PRE_RESUME phase below)
	extReg.Register("validator", &mockExtension{
		executeFunc: func(ctx context.Context, record *store.TaskRecord, payload map[string]any, properties json.RawMessage) error {
			preExecuted = true
			if payload["age"].(float64) < 18 {
				return errors.New("underage")
			}
			// attempt to mutate the payload; mutations must be discarded (read-only contract)
			payload["checked"] = true
			return nil
		},
	})

	// Register a logger extension (wired to the POST_RESUME phase below)
	extReg.Register("logger", &mockExtension{
		executeFunc: func(ctx context.Context, record *store.TaskRecord, payload map[string]any, properties json.RawMessage) error {
			postExecuted = true
			postExecutedWg.Done()
			return nil
		},
	})

	tm := NewTaskManager(db, registry, newTestPluginsRegistry(), extReg, &mockTemporalManager{}, noopCallback, noopRenderer{})

	// Setup a task record with active subtask configuration
	record := store.TaskRecord{
		TaskID:               "test-task-ext",
		TaskType:             "TEST",
		State:                "PENDING_USER",
		ActiveTaskTemplateID: "generic_user_input_with_extensions",
		TaskWorkflowID:       "wf-123",
		TaskRunID:            "run-123",
		SubTaskNodeID:        "node-123",
	}
	db.SaveTask(context.Background(), record)

	// 1. Test failing validation (blocking)
	err := tm.CompleteTaskStep(context.Background(), "test-task-ext", map[string]any{"age": 16.0})
	if err == nil {
		t.Fatal("expected error due to underage payload, got nil")
	}
	if preExecuted == false {
		t.Error("pre-resume extension was not executed")
	}
	if postExecuted == true {
		t.Error("post-resume extension should not have executed on failure")
	}

	// Verify DB is not updated and Temporal task done not called
	updatedRecord, _ := db.GetTask(context.Background(), "test-task-ext")
	if updatedRecord.Data["form"] != nil {
		t.Error("expected DB not to be updated on failed validation")
	}

	// Reset execution flag
	preExecuted = false

	// Mock temporal manager to check if TaskDone is called
	taskDoneCalled := false
	tm.taskWorkflowManager = &mockTemporalManager{
		taskDoneFunc: func(ctx context.Context, workflowID, runID, activityID string, result map[string]any) error {
			taskDoneCalled = true
			return nil
		},
	}

	// 2. Test successful validation (continues to POST_RESUME)
	err = tm.CompleteTaskStep(context.Background(), "test-task-ext", map[string]any{"age": 20.0})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if preExecuted == false {
		t.Error("pre-resume extension was not executed on success")
	}
	if !taskDoneCalled {
		t.Error("expected Temporal TaskDone to be called")
	}

	// Wait for async POST_RESUME extension to execute
	postExecutedWg.Wait()
	if postExecuted == false {
		t.Error("post-resume extension was not executed")
	}

	// Verify DB state is updated, and that the PRE_RESUME extension's mutation
	// attempt was discarded (extensions receive a read-only copy of the payload).
	updatedRecord, _ = db.GetTask(context.Background(), "test-task-ext")
	formData := updatedRecord.Data["form"].(map[string]any)
	if formData["age"] != 20.0 {
		t.Errorf("expected age to be 20, got %v", formData["age"])
	}
	if _, mutated := formData["checked"]; mutated {
		t.Error("expected PRE_RESUME payload mutation to be discarded, but 'checked' was persisted")
	}
}
