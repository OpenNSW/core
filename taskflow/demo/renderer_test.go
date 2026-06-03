package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	engine "github.com/OpenNSW/go-temporal-workflow"
	"github.com/OpenNSW/nsw-task-flow/orchestrator"
	"github.com/OpenNSW/nsw-task-flow/plugins"
	"github.com/OpenNSW/nsw-task-flow/renderer"
	"github.com/OpenNSW/nsw-task-flow/store"
	"go.temporal.io/sdk/activity"
)

// ---------------------------------------------------------------------------
// Hermetic in-memory store for the pipeline test — avoids the file-backed
// TaskDB which persists across runs to /tmp.
// ---------------------------------------------------------------------------

type memStore struct {
	tasks map[string]store.TaskRecord
}

func newMemStore() *memStore {
	return &memStore{tasks: make(map[string]store.TaskRecord)}
}
func (m *memStore) SaveTask(_ context.Context, r store.TaskRecord) {
	m.tasks[r.TaskID] = r
}
func (m *memStore) GetTask(_ context.Context, id string) (store.TaskRecord, bool) {
	r, ok := m.tasks[id]
	return r, ok
}
func (m *memStore) GetTaskByWorkflowID(_ context.Context, wf string) (store.TaskRecord, bool) {
	for _, r := range m.tasks {
		if r.TaskWorkflowID == wf {
			return r, true
		}
	}
	return store.TaskRecord{}, false
}
func (m *memStore) GetAllTasks(_ context.Context, parentWorkflowID string) []store.TaskRecord {
	out := make([]store.TaskRecord, 0, len(m.tasks))
	for _, r := range m.tasks {
		if parentWorkflowID != "" && r.ParentWorkflowID != parentWorkflowID {
			continue
		}
		out = append(out, r)
	}
	return out
}

// ---------------------------------------------------------------------------
// Unit tests for SimpleRenderer
// ---------------------------------------------------------------------------

func TestSimpleRenderer_StateKeyedConfig(t *testing.T) {
	cfg := json.RawMessage(`{
		"PENDING_USER": {"primary": {"type": "markdown", "payload": "fill the form"}},
		"COMPLETED":    {"primary": {"type": "markdown", "payload": "done"}},
		"default":      {"primary": {"type": "markdown", "payload": "fallback"}}
	}`)

	cases := []struct {
		name        string
		state       string
		wantType    string
		wantPayload string
	}{
		{"matching state", "PENDING_USER", "markdown", "fill the form"},
		{"another matching state", "COMPLETED", "markdown", "done"},
		{"unknown state falls back to default", "WHO_KNOWS", "markdown", "fallback"},
	}

	r := SimpleRenderer{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rawOut, err := r.Render(context.Background(), cfg, renderer.Facts{State: tc.state})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			var out RenderResult
			if err := json.Unmarshal(rawOut, &out); err != nil {
				t.Fatalf("unmarshal RenderResult: %v", err)
			}
			got, ok := out["primary"]
			if !ok {
				t.Fatalf("missing slot 'primary' in %v", out)
			}
			if got.Type != tc.wantType {
				t.Errorf("type: want %q, got %q", tc.wantType, got.Type)
			}
			var payload string
			if err := json.Unmarshal(got.Payload, &payload); err != nil {
				t.Fatalf("payload unmarshal: %v", err)
			}
			if payload != tc.wantPayload {
				t.Errorf("payload: want %q, got %q", tc.wantPayload, payload)
			}
		})
	}
}

func TestSimpleRenderer_EmptyConfig(t *testing.T) {
	rawOut, err := SimpleRenderer{}.Render(context.Background(), nil, renderer.Facts{State: "ANY"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(rawOut) != 0 {
		t.Errorf("expected empty result, got %s", rawOut)
	}
}

func TestSimpleRenderer_NoMatchAndNoDefault(t *testing.T) {
	cfg := json.RawMessage(`{"OTHER": {"primary": {"type": "markdown", "payload": "x"}}}`)
	rawOut, err := SimpleRenderer{}.Render(context.Background(), cfg, renderer.Facts{State: "NOT_PRESENT"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(rawOut) != 0 {
		t.Errorf("expected empty result with no default, got %s", rawOut)
	}
}

func TestSimpleRenderer_MalformedConfig(t *testing.T) {
	_, err := SimpleRenderer{}.Render(context.Background(), json.RawMessage(`{not json`), renderer.Facts{State: "X"})
	if err == nil {
		t.Fatal("expected error for malformed config")
	}
}

// ---------------------------------------------------------------------------
// End-to-end pipeline test
//
// Wires the real demo registry, the real SimpleRenderer, the real in-memory
// TaskDB, and the real TaskManager. The Temporal manager is stubbed out
// because we are only checking the render pipeline, not workflow execution.
// ---------------------------------------------------------------------------

type stubTemporal struct{}

func (stubTemporal) StartWorkflow(_ context.Context, _ string, _ engine.WorkflowDefinition, _ map[string]any) error {
	return nil
}
func (stubTemporal) TaskDone(_ context.Context, _, _, _ string, _ map[string]any) error {
	return nil
}
func (stubTemporal) StartWorker() error { return nil }
func (stubTemporal) StopWorker()        {}
func (stubTemporal) TaskUpdate(_ context.Context, _, _ string, _ engine.UpdateEvent) error {
	return nil
}
func (stubTemporal) GetStatus(_ context.Context, _ string) (*engine.WorkflowInstance, error) {
	return nil, nil
}

func TestPipeline_EndToEnd_TaskRender(t *testing.T) {
	// 1. Load the real demo templates from disk (templates/ is relative to the
	// demo package directory when running `go test ./demo/...`).
	registry := NewTemplateRegistry()
	if err := loadTemplates(registry, "templates"); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	// Sanity-check that the bits we depend on actually loaded.
	if _, ok := registry.GetTaskTemplate("demo_phyto_application_task"); !ok {
		t.Fatal("demo_phyto_application_task task template not loaded")
	}
	if _, ok := registry.GetWorkflow("Phyto_Application_Flow_v1"); !ok {
		t.Fatal("Phyto_Application_Flow_v1 workflow not loaded")
	}
	if _, ok := registry.GetGenericTemplate("phyto_render_config"); !ok {
		t.Fatal("phyto_render_config render config not loaded")
	}

	// 2. Wire everything together with a stubbed Temporal manager and a
	// hermetic in-memory task store.
	db := newMemStore()
	pluginsReg := plugins.NewRegistry()
	tm := orchestrator.NewTaskManager(
		db,
		registry,
		pluginsReg,
		stubTemporal{},
		func(_, _, _ string, _ map[string]any) error { return nil },
		SimpleRenderer{},
	)

	// 3. Start a task. StartTask returns activity.ErrResultPending on the happy
	// path because the workflow is still running — that's expected here.
	_, err := tm.StartTask(engine.TaskPayload{
		WorkflowID:     "parent-wf",
		RunID:          "parent-run",
		NodeID:         "parent-node",
		TaskTemplateID: "demo_phyto_application_task",
	})
	if err != nil && !errors.Is(err, activity.ErrResultPending) {
		t.Fatalf("StartTask: %v", err)
	}

	// 4. List tasks (summary, no View), then drill in for the rendered detail
	// and assert it matches the markdown for the STARTING state.
	views := tm.GetAllTasks(context.Background(), "")
	if len(views) != 1 {
		t.Fatalf("want 1 task view, got %d", len(views))
	}
	summary := views[0]

	if summary.State != "STARTING" {
		t.Errorf("State: want STARTING, got %s", summary.State)
	}
	if summary.TaskType != "APPLICATION" {
		t.Errorf("TaskType: want APPLICATION, got %s", summary.TaskType)
	}

	v, err := tm.GetTaskRenderInfo(context.Background(), summary.TaskID)
	if err != nil {
		t.Fatalf("GetTaskRenderInfo: %v", err)
	}

	var view RenderResult
	if err := json.Unmarshal(v.View, &view); err != nil {
		t.Fatalf("unmarshal view: %v", err)
	}

	comp, ok := view["primary"]
	if !ok {
		t.Fatalf("missing slot 'primary' in rendered view: %s", v.View)
	}
	if comp.Type != "markdown" {
		t.Errorf("component type: want markdown, got %s", comp.Type)
	}
	var payload string
	if err := json.Unmarshal(comp.Payload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	wantPayload := "Starting your phyto application…"
	if payload != wantPayload {
		t.Errorf("payload: want %q, got %q", wantPayload, payload)
	}
}
