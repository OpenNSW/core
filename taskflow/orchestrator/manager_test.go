package orchestrator

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	engine "github.com/OpenNSW/go-temporal-workflow"
	"github.com/OpenNSW/nsw-task-flow/plugins"
	"github.com/OpenNSW/nsw-task-flow/store"
)

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

type safeMockTaskStore struct {
	mu    sync.RWMutex
	tasks map[string]store.TaskRecord
}

func newSafeMockTaskStore() *safeMockTaskStore {
	return &safeMockTaskStore{
		tasks: make(map[string]store.TaskRecord),
	}
}

func (s *safeMockTaskStore) SaveTask(task store.TaskRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.TaskID] = task
}

func (s *safeMockTaskStore) GetTask(taskID string) (store.TaskRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, exists := s.tasks[taskID]
	return task, exists
}

func (s *safeMockTaskStore) GetTaskByWorkflowID(workflowID string) (store.TaskRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tasks {
		if t.TaskWorkflowID == workflowID {
			return t, true
		}
	}
	return store.TaskRecord{}, false
}

func (s *safeMockTaskStore) GetAllTasks() []store.TaskRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]store.TaskRecord, 0, len(s.tasks))
	for _, r := range s.tasks {
		out = append(out, r)
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestPluginsRegistry() *plugins.Registry {
	pr := plugins.NewRegistry()
	pr.Register("TEST", plugins.NewUserInputPlugin())

	// Use an offline-friendly mock HTTP dispatcher for unit tests to avoid real HTTP requests
	mockDispatcher := func(ctx context.Context, url string, taskID string, payload map[string]any) error {
		return nil
	}
	pr.Register("TEST", plugins.NewExternalReviewPlugin(mockDispatcher))
	return pr
}

func newTestRegistry() *TaskTemplateRegistry {
	r := NewTaskTemplateRegistry()
	r.Register(TaskTemplateEntry{
		TemplateID:       "test_template",
		TaskType:         "TEST",
		WorkflowID:       "test_workflow_v1",
		PluginName:       "generic_user_input",
		PluginProperties: []byte(`{"user_jsonforms_id": "user_form"}`),
	})
	r.Register(TaskTemplateEntry{
		TemplateID:       "generic_user_input",
		TaskType:         "TEST",
		WorkflowID:       "test_workflow_v1",
		PluginName:       "generic_user_input",
		PluginProperties: []byte(`{"user_jsonforms_id": "user_form"}`),
	})
	r.Register(TaskTemplateEntry{
		TemplateID:       "generic_external_review",
		TaskType:         "TEST",
		WorkflowID:       "test_workflow_v1",
		PluginName:       "generic_external_review",
		PluginProperties: []byte(`{"reviewer_jsonforms_id": "reviewer_form", "external_url": "http://localhost/review"}`),
	})
	return r
}

func newTestTaskManager(db store.TaskStore, registry *TaskTemplateRegistry, tm engine.TemporalManager, cb TaskCompletedCallback) *TaskManager {
	return NewTaskManager(db, registry, newTestPluginsRegistry(), tm, cb)
}

func writeTempTaskJSON(t *testing.T, content string) (path string, cleanup func()) {
	t.Helper()
	f, err := os.CreateTemp("", "task_*.json")
	if err != nil {
		t.Fatalf("could not create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("could not write temp file: %v", err)
	}
	f.Close()
	return f.Name(), func() { os.Remove(f.Name()) }
}

func noopCallback(_, _, _ string, _ map[string]any) error { return nil }

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

	path, cleanup := writeTempTaskJSON(t, `{"id": "test_workflow_v1", "nodes": []}`)
	defer cleanup()

	tm := newTestTaskManager(storeMock, registry, mockTaskWFManager, onCompleted).WithTaskDefPath(path)

	// 1. StartTask
	payload := engine.TaskPayload{
		WorkflowID:     "parent-workflow",
		RunID:          "parent-run",
		NodeID:         "node-1",
		TaskTemplateID: "test_template",
		Inputs:         map[string]any{"userform.name": "Alice"},
	}

	if err := tm.StartTask(payload); err != nil {
		t.Fatalf("StartTask failed: %v", err)
	}
	if !taskWorkflowCalled {
		t.Error("expected task sub-workflow to be started")
	}

	tasks := storeMock.GetAllTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected exactly 1 task record, got %d", len(tasks))
	}
	task := tasks[0]
	if task.Status != "STARTING" {
		t.Errorf("expected status 'STARTING', got '%s'", task.Status)
	}
	if task.ParentWorkflowID != "parent-workflow" {
		t.Errorf("expected parent workflow 'parent-workflow', got '%s'", task.ParentWorkflowID)
	}

	// 2. StartSubTask — generic_user_input
	payloadTaskWF := engine.TaskPayload{
		WorkflowID:     task.TaskWorkflowID,
		RunID:          "task-run",
		NodeID:         "task-node",
		TaskTemplateID: "generic_user_input",
	}
	if err := tm.StartSubTask(payloadTaskWF); err != nil {
		t.Fatalf("StartSubTask failed: %v", err)
	}

	task, _ = storeMock.GetTask(task.TaskID)
	if task.Status != "PENDING_USER" {
		t.Errorf("expected status 'PENDING_USER', got '%s'", task.Status)
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

	userData := map[string]any{
		"userform": map[string]any{
			"applicant_name": "Alice",
			"email":          "alice@example.com",
		},
	}
	if err := tm.CompleteTaskStep(context.Background(), task.TaskID, userData); err != nil {
		t.Fatalf("CompleteTaskStep failed: %v", err)
	}
	if !taskDoneCalled {
		t.Error("expected TaskDone to be called on task workflow")
	}

	task, _ = storeMock.GetTask(task.TaskID)
	userform, ok := task.Data["userform"].(map[string]any)
	if !ok {
		t.Fatal("expected 'userform' to be a nested map")
	}
	if userform["email"] != "alice@example.com" {
		t.Errorf("expected userform.email 'alice@example.com', got '%v'", userform["email"])
	}

	// 4. HandleTaskCompletion
	finalVars := map[string]any{"reviewerform.review_outcome": "approve"}
	if err := tm.HandleTaskCompletion(task.TaskWorkflowID, finalVars); err != nil {
		t.Fatalf("HandleTaskCompletion failed: %v", err)
	}

	task, _ = storeMock.GetTask(task.TaskID)
	if task.Status != "COMPLETED" {
		t.Errorf("expected task status 'COMPLETED', got '%s'", task.Status)
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

	err := tm.StartTask(engine.TaskPayload{
		WorkflowID:     "parent-wf",
		TaskTemplateID: "does_not_exist",
	})
	if err == nil {
		t.Fatal("expected error for unknown template ID, got nil")
	}
}

func TestStartTask_MissingTaskDefFile(t *testing.T) {
	tm := newTestTaskManager(newSafeMockTaskStore(), newTestRegistry(), &mockTemporalManager{}, noopCallback).
		WithTaskDefPath("/tmp/this_file_does_not_exist_xyz.json")

	err := tm.StartTask(engine.TaskPayload{
		WorkflowID:     "parent-wf",
		TaskTemplateID: "test_template",
	})
	if err == nil {
		t.Fatal("expected error for missing task def file, got nil")
	}
}

func TestStartTask_MalformedTaskDefFile(t *testing.T) {
	path, cleanup := writeTempTaskJSON(t, `{not valid json`)
	defer cleanup()

	tm := newTestTaskManager(newSafeMockTaskStore(), newTestRegistry(), &mockTemporalManager{}, noopCallback).
		WithTaskDefPath(path)

	err := tm.StartTask(engine.TaskPayload{
		WorkflowID:     "parent-wf",
		TaskTemplateID: "test_template",
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestStartTask_TaskWorkflowManagerError(t *testing.T) {
	mockTaskWF := &mockTemporalManager{
		startWorkflowFunc: func(_ context.Context, _ string, _ engine.WorkflowDefinition, _ map[string]any) error {
			return errors.New("temporal unavailable")
		},
	}

	path, cleanup := writeTempTaskJSON(t, `{"id":"wf","nodes":[]}`)
	defer cleanup()

	tm := newTestTaskManager(newSafeMockTaskStore(), newTestRegistry(), mockTaskWF, noopCallback).
		WithTaskDefPath(path)

	err := tm.StartTask(engine.TaskPayload{
		WorkflowID:     "parent-wf",
		TaskTemplateID: "test_template",
	})
	if err == nil {
		t.Fatal("expected error when task StartWorkflow fails, got nil")
	}
}

// ---------------------------------------------------------------------------
// StartSubTask — error paths
// ---------------------------------------------------------------------------

func TestStartSubTask_UnknownWorkflowID(t *testing.T) {
	tm := newTestTaskManager(newSafeMockTaskStore(), newTestRegistry(), &mockTemporalManager{}, noopCallback)

	err := tm.StartSubTask(engine.TaskPayload{
		WorkflowID:     "workflow-that-was-never-registered",
		TaskTemplateID: "generic_user_input",
	})
	if err == nil {
		t.Fatal("expected error for unknown workflow ID, got nil")
	}
}

func TestStartSubTask_UnknownTaskTemplateID(t *testing.T) {
	db := newSafeMockTaskStore()
	db.SaveTask(store.TaskRecord{
		TaskID:         "task-1",
		TaskWorkflowID: "task-workflow-1",
		Status:         "STARTING",
		Data:           map[string]any{},
	})

	tm := newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{}, noopCallback)

	err := tm.StartSubTask(engine.TaskPayload{
		WorkflowID:     "task-workflow-1",
		TaskTemplateID: "not_a_real_template",
	})
	if err == nil {
		t.Fatal("expected error for unknown task_template_id in StartSubTask, got nil")
	}
}

func TestStartSubTask_ExternalReviewPath(t *testing.T) {
	db := newSafeMockTaskStore()
	db.SaveTask(store.TaskRecord{
		TaskID:         "task-ext",
		TaskWorkflowID: "task-ext-workflow",
		Status:         "STARTING",
		Data:           map[string]any{},
	})

	tm := newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{}, noopCallback)

	err := tm.StartSubTask(engine.TaskPayload{
		WorkflowID:     "task-ext-workflow",
		RunID:          "run-1",
		NodeID:         "node-ext",
		TaskTemplateID: "generic_external_review",
	})
	if err != nil {
		t.Fatalf("StartSubTask for generic_external_review failed: %v", err)
	}

	task, _ := db.GetTask("task-ext")
	if task.Status != "QUEUED_EXTERNALLY" {
		t.Errorf("expected status QUEUED_EXTERNALLY, got %s", task.Status)
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
	db.SaveTask(store.TaskRecord{
		TaskID: "task-done",
		Status: "COMPLETED",
		Data:   map[string]any{},
	})

	tm := newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{}, noopCallback)

	err := tm.CompleteTaskStep(context.Background(), "task-done", map[string]any{"x": 1})
	if err == nil {
		t.Fatal("expected error for already-completed task, got nil")
	}
}

// ---------------------------------------------------------------------------
// HandleTaskCompletion — edge cases
// ---------------------------------------------------------------------------

func TestHandleTaskCompletion_UnknownWorkflowID_ReturnsNil(t *testing.T) {
	tm := newTestTaskManager(newSafeMockTaskStore(), newTestRegistry(), &mockTemporalManager{}, noopCallback)

	err := tm.HandleTaskCompletion("unknown-workflow", map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("expected nil for unknown workflow, got: %v", err)
	}
}

func TestHandleTaskCompletion_CallbackError_TaskStillMarkedCompleted(t *testing.T) {
	db := newSafeMockTaskStore()
	db.SaveTask(store.TaskRecord{
		TaskID:         "task-cb-err",
		TaskWorkflowID: "task-cb-workflow",
		Status:         "PENDING_USER",
		Data:           map[string]any{},
	})

	callbackErr := errors.New("parent unreachable")
	tm := newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{},
		func(_, _, _ string, _ map[string]any) error { return callbackErr },
	)

	err := tm.HandleTaskCompletion("task-cb-workflow", map[string]any{})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("expected callback error to be propagated, got: %v", err)
	}

	task, _ := db.GetTask("task-cb-err")
	if task.Status != "COMPLETED" {
		t.Errorf("expected status COMPLETED even after callback error, got %s", task.Status)
	}
}

// ---------------------------------------------------------------------------
// setNestedKey — unit tests
// ---------------------------------------------------------------------------

func TestSetNestedKey_SingleLevel(t *testing.T) {
	m := map[string]any{}
	setNestedKey(m, "name", "Alice")
	if m["name"] != "Alice" {
		t.Errorf("expected 'Alice', got %v", m["name"])
	}
}

func TestSetNestedKey_TwoLevels(t *testing.T) {
	m := map[string]any{}
	setNestedKey(m, "user.email", "alice@example.com")

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
	setNestedKey(m, "a.b.c", 42)

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
	setNestedKey(m, "", "should_not_appear")
	if len(m) != 1 {
		t.Errorf("expected map to be unchanged, got %v", m)
	}
}

func TestSetNestedKey_OverwritesNonMapIntermediate(t *testing.T) {
	m := map[string]any{"user": "not-a-map"}
	setNestedKey(m, "user.email", "alice@example.com")

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
	setNestedKey(m, "user.email", "bob@example.com")

	sub := m["user"].(map[string]any)
	if sub["name"] != "Bob" {
		t.Error("existing key 'name' should not be removed")
	}
	if sub["email"] != "bob@example.com" {
		t.Errorf("expected 'bob@example.com', got %v", sub["email"])
	}
}
