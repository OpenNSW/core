package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifactadapter/generictemplate"
	"github.com/OpenNSW/core/artifactadapter/subtasktemplate"
	"github.com/OpenNSW/core/artifactadapter/tasktemplate"
	"github.com/OpenNSW/core/artifactadapter/workflowdef"
	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/renderer"
	"github.com/OpenNSW/core/taskflow/store"
	engine "github.com/OpenNSW/core/workflow"
	"go.temporal.io/sdk/activity"
)

/*
Package orchestrator provides a domain-driven TaskManager designed to decouple high-level
macro journeys from low-level interactive processes.

The system uses a hierarchical, decoupled design:

1. Workflow (Macro Journey):
   The high-level orchestrating workflow (parent workflow). When the macro journey hits a
   "Task" node, it executes a callback that calls TaskManager.StartTask().

2. Task (Micro Journey):
   A self-contained micro-flow executing child tasks (such as document upload, fee payment,
   or physical inspections). The Task runs as an independent workflow process under the hood
   (defined by a JSON workflow definition).

3. SubTask (Interaction Steps):
   Individual, potentially asynchronous execution nodes inside the Task (e.g., waiting for
   a user form submission, or queuing a request in an external agency portal). These are
   dispatched via StartSubTask() and resumed via CompleteTaskStep().

Flow Diagram:
              [Parent Workflow]
                     │
                     ▼ (StartTask)
              [TaskManager] ────► [Task Record created in DB]
                     │
                     ▼ (StartTaskWorkflow)
              [Task Workflow]
                     │
                     ▼ (StartSubTask)
              [SubTask Node] (e.g., PENDING_USER status)
                     │
                     ▼ (CompleteTaskStep)
           [Resume SubTask & Continue]
                     │
                     ▼ (TaskWorkflow completed)
           [HandleTaskCompletion]
                     │
                     ▼ (Callback)
              [Resume Parent Workflow]
*/

// TaskCompletedCallback is a callback function invoked when a Task workflow completes.
// It is typically used to wake up the parent workflow with the final task output variables.
type TaskCompletedCallback func(parentWorkflowID string, parentRunID string, parentNodeID string, finalVariables map[string]any) error

// TaskManager orchestrates decoupled tasks and interactions under parent workflows.
// It bridges macro-level workflows and micro-level interactive tasks via a single DB entry per task.
type TaskManager struct {
	db                  store.TaskStore
	renderer            renderer.Renderer
	registry            *artifact.Registry
	pluginsRegistry     *plugins.Registry
	onTaskCompleted     TaskCompletedCallback
	taskWorkflowManager engine.TemporalManager
}

// NewTaskManager creates a TaskManager instance.
//
//   - db                  — the persistence/in-memory task store.
//   - registry            — artifact registry holding task templates, subtask templates, workflow definitions, and render configs.
//   - pluginsRegistry     — registry containing task execution plugin handlers.
//   - taskWorkflowManager — the TemporalManager used to start and complete Task sub-workflows.
//   - onTaskCompleted     — callback invoked when a Task workflow finishes;
//     typically invokes Parent.TaskDone to resume the parent workflow using stored coordinates.
func NewTaskManager(
	db store.TaskStore,
	registry *artifact.Registry,
	pluginsRegistry *plugins.Registry,
	taskWorkflowManager engine.TemporalManager,
	onTaskCompleted TaskCompletedCallback,
	renderer renderer.Renderer,
) *TaskManager {
	return &TaskManager{
		db:                  db,
		registry:            registry,
		pluginsRegistry:     pluginsRegistry,
		onTaskCompleted:     onTaskCompleted,
		taskWorkflowManager: taskWorkflowManager,
		renderer:            renderer,
	}
}

// StartTask is called by the parent workflow engine when it activates a TASK node.
// It looks up the template registry, creates a single DB record with parent
// coordinates, and kicks off the Task's internal workflow.
func (tm *TaskManager) StartTask(ctx context.Context, payload engine.TaskPayload) (map[string]any, error) {
	template, err := tasktemplate.Load(ctx, tm.registry, payload.TaskTemplateID)
	if err != nil {
		return nil, fmt.Errorf("load task template %q: %w", payload.TaskTemplateID, err)
	}

	wfDef, err := workflowdef.Load(ctx, tm.registry, template.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("load workflow %q referenced by task template %q: %w", template.WorkflowID, template.ID, err)
	}

	renderConfig, err := generictemplate.Load(ctx, tm.registry, template.RenderConfigID)
	if err != nil {
		return nil, fmt.Errorf("load render config %q referenced by task template %q: %w", template.RenderConfigID, template.ID, err)
	}

	// Use the parent workflow node ID as the TaskID. It must be globally unique. Callers that know the node ID can address
	// the task without a lookup.
	taskID := payload.NodeID
	taskWorkflowID := "task-wf-" + taskID

	initialData := make(map[string]any)
	for k, v := range payload.Inputs {
		setNestedKey(initialData, k, v)
	}
	initialData["_task_id"] = taskID

	record := store.TaskRecord{
		TaskID:           taskID,
		TaskType:         template.Type,
		State:            "STARTING",
		RenderConfig:     renderConfig,
		ParentWorkflowID: payload.WorkflowID,
		ParentRunID:      payload.RunID,
		ParentNodeID:     payload.NodeID,
		TaskWorkflowID:   taskWorkflowID,
		Data:             initialData,
		CreatedAt:        time.Now(),
	}
	tm.db.SaveTask(ctx, record)
	log.Printf("[TaskManager] Created Task record %s (template=%s, type=%s)", taskID, payload.TaskTemplateID, template.Type)

	// Verify that there are no parallel execution paths, as TaskRecord only stores coordinates for a single active subtask.
	for _, node := range wfDef.Nodes {
		if node.Type == engine.NodeTypeGateway &&
			(node.GatewayType == engine.GatewayTypeParallelSplit || node.GatewayType == "INCLUSIVE_SPLIT") {
			return nil, fmt.Errorf("parallel subtasks are not supported: task workflow %s contains parallel gateway %s (%s)", wfDef.ID, node.ID, node.GatewayType)
		}
	}

	err = tm.taskWorkflowManager.StartWorkflow(ctx, taskWorkflowID, wfDef, initialData)
	if err != nil {
		return nil, fmt.Errorf("failed to start task workflow: %v", err)
	}
	log.Printf("[TaskManager] Started task workflow %s for task %s", taskWorkflowID, taskID)
	return nil, activity.ErrResultPending
}

// StartSubTask is called by the Task's workflow engine when it activates an interaction step.
// It routes to the correct capability handler dynamically from the plugin registry.
func (tm *TaskManager) StartSubTask(ctx context.Context, payload engine.TaskPayload) (map[string]any, error) {
	record, exists := tm.db.GetTaskByWorkflowID(ctx, payload.WorkflowID)
	if !exists {
		return nil, fmt.Errorf("[StartSubTask] no task record found for workflow %s", payload.WorkflowID)
	}

	record.TaskRunID = payload.RunID
	record.SubTaskNodeID = payload.NodeID
	record.ActiveTaskTemplateID = payload.TaskTemplateID

	for k, v := range payload.Inputs {
		setNestedKey(record.Data, k, v)
	}

	// 1. Look up the subtask template to find the associated plugin config
	subTemplate, err := subtasktemplate.Load(ctx, tm.registry, payload.TaskTemplateID)
	if err != nil {
		return nil, fmt.Errorf("[StartSubTask] load subtask template %q: %w", payload.TaskTemplateID, err)
	}
	record.ActiveOutputNamespace = subTemplate.OutputNamespace

	// 2. Fetch the plugin from our registry using TaskType
	plugin, ok := tm.pluginsRegistry.Get(subTemplate.TaskType)
	if !ok {
		return nil, fmt.Errorf("[StartSubTask] unregistered plugin for task type %s (required for template: %s)", subTemplate.TaskType, payload.TaskTemplateID)
	}

	// 3. Execute the plugin
	pluginCtx := plugins.PluginContext{
		Context: ctx,
		Record:  &record,
		Inputs:  payload.Inputs,
	}

	err = plugin.Execute(pluginCtx, subTemplate.PluginProperties)
	if errors.Is(err, plugins.ErrSuspended) {
		tm.db.SaveTask(pluginCtx.Context, record)
		return nil, activity.ErrResultPending
	}
	if err != nil {
		return nil, fmt.Errorf("[StartSubTask] plugin for task type %q execution failed: %w", subTemplate.TaskType, err)
	}

	tm.db.SaveTask(pluginCtx.Context, record)

	// Otherwise, this step completed synchronously. Return its modified payload immediately to transition directly.
	return record.Data, nil
}

// HandleTaskCompletion is called when a Task workflow hits its END node.
// It marks the task complete and fires the onTaskCompleted callback to resume the parent workflow.
func (tm *TaskManager) HandleTaskCompletion(ctx context.Context, workflowID string, finalVariables map[string]any) error {
	record, exists := tm.db.GetTaskByWorkflowID(ctx, workflowID)
	if !exists {
		// Not a workflow we own — safe to ignore.
		return nil
	}

	log.Printf("[TaskManager] Task workflow %s completed for task %s", workflowID, record.TaskID)

	record.State = "COMPLETED"
	tm.db.SaveTask(ctx, record)

	err := tm.onTaskCompleted(record.ParentWorkflowID, record.ParentRunID, record.ParentNodeID, finalVariables)
	if err != nil {
		log.Printf("[TaskManager] Failed to execute task completion callback for %s: %v", record.TaskID, err)
		return err
	}

	log.Printf("[TaskManager] Successfully processed completion for task %s", record.TaskID)
	return nil
}

// CompleteTaskStep is the public API for external clients or portals to submit form/interaction
// data and resume the active step in the corresponding Task workflow.
func (tm *TaskManager) CompleteTaskStep(ctx context.Context, taskID string, payload map[string]any) error {
	record, exists := tm.db.GetTask(ctx, taskID)
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	if record.State == "COMPLETED" {
		return fmt.Errorf("task %s already completed", taskID)
	}

	if record.Data == nil {
		record.Data = make(map[string]any)
	}

	// Writes are confined to the active subtask's declared OutputNamespace,
	// which was snapshotted onto the record by StartSubTask. An open
	// top-level merge would let callers overwrite slots owned by other
	// subtasks (or internal keys like _task_id), so the namespace is
	// required for any non-empty payload. If it's missing we log loudly and
	// drop the payload — the workflow still resumes so a misconfigured
	// template doesn't break a running task.
	if len(payload) > 0 {
		if record.ActiveOutputNamespace == "" {
			log.Printf("[TaskManager] WARNING: task %s active subtask (template=%q) declares no output_namespace; dropping submission payload (keys=%v)", taskID, record.ActiveTaskTemplateID, payloadKeys(payload))
		} else {
			record.Data[record.ActiveOutputNamespace] = payload
		}
	}
	tm.db.SaveTask(ctx, record)

	log.Printf("[TaskManager] Waking active activity %s in workflow %s (task %s)",
		record.SubTaskNodeID, record.TaskWorkflowID, taskID)

	err := tm.taskWorkflowManager.TaskDone(
		ctx,
		record.TaskWorkflowID,
		record.TaskRunID,
		record.SubTaskNodeID,
		payload, // pass full namespaced state back to the workflow
	)
	if err != nil {
		return fmt.Errorf("failed to resume task workflow: %w", err)
	}

	return nil
}

// GetTaskRenderInfo retrieves a task record and dynamically decorates it with rich render metadata
// (like JSON schemas) fetched on-the-fly from its executing plugin.
func (tm *TaskManager) GetTaskRenderInfo(context context.Context, taskID string) (TaskView, error) {
	record, exists := tm.db.GetTask(context, taskID)
	if !exists {
		return TaskView{}, fmt.Errorf("task record %s not found", taskID)
	}

	view, err := tm.renderer.Render(context, record.RenderConfig, renderer.Facts{State: record.State, Data: record.Data})
	if err != nil {
		return TaskView{}, fmt.Errorf("rendering task %s: %w", taskID, err)
	}

	res := TaskView{
		TaskID:    record.TaskID,
		TaskType:  record.TaskType,
		State:     record.State,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
		View:      view, // actually attach the render output
	}

	return res, nil
}

// GetAllTasks returns a lightweight summary of tasks for listing purposes. The View
// field is intentionally not populated — callers should use GetTaskRenderInfo to fetch
// the full rendered view for a specific task.
//
// If parentWorkflowID is non-empty, the result is narrowed to tasks spawned by that
// parent workflow; an empty string returns all tasks.
func (tm *TaskManager) GetAllTasks(ctx context.Context, parentWorkflowID string) []TaskView {
	records := tm.db.GetAllTasks(ctx, parentWorkflowID)
	resList := make([]TaskView, 0, len(records))
	for _, r := range records {
		resList = append(resList, TaskView{
			TaskID:    r.TaskID,
			TaskType:  r.TaskType,
			State:     r.State,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		})
	}
	return resList
}

func payloadKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	return keys
}
