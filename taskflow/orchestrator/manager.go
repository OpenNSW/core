package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"time"

	engine "github.com/OpenNSW/go-temporal-workflow"
	"github.com/OpenNSW/nsw-task-flow/plugins"
	"github.com/OpenNSW/nsw-task-flow/renderer"
	"github.com/OpenNSW/nsw-task-flow/store"
	"github.com/google/uuid"
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
	registry            TaskTemplateRegistry
	pluginsRegistry     *plugins.Registry
	onTaskCompleted     TaskCompletedCallback
	taskWorkflowManager engine.TemporalManager
}

// NewTaskManager creates a TaskManager instance.
//
//   - db                  — the persistence/in-memory task store.
//   - registry            — registry holding definitions of task capabilities.
//   - pluginsRegistry     — registry containing task execution plugin handlers.
//   - taskWorkflowManager — the TemporalManager used to start and complete Task sub-workflows.
//   - onTaskCompleted     — callback invoked when a Task workflow finishes;
//     typically invokes Parent.TaskDone to resume the parent workflow using stored coordinates.
func NewTaskManager(
	db store.TaskStore,
	registry TaskTemplateRegistry,
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
func (tm *TaskManager) StartTask(payload engine.TaskPayload) (map[string]any, error) {
	template, ok := tm.registry.GetTaskTemplate(payload.TaskTemplateID)
	if !ok {
		return nil, fmt.Errorf("unknown task_template_id: %s", payload.TaskTemplateID)
	}

	wfDef, ok := tm.registry.GetWorkflow(template.WorkflowID)
	if !ok {
		return nil, fmt.Errorf("workflow %q referenced by task template %q not registered", template.WorkflowID, template.ID)
	}

	renderConfig, ok := tm.registry.GetGenericTemplate(template.RenderConfigID)
	if !ok {
		return nil, fmt.Errorf("render config %q referenced by task template %q not registered", template.RenderConfigID, template.ID)
	}

	taskID := "task-" + uuid.New().String()[:8]
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
	tm.db.SaveTask(record)
	log.Printf("[TaskManager] Created Task record %s (template=%s, type=%s)", taskID, payload.TaskTemplateID, template.Type)

	// Verify that there are no parallel execution paths, as TaskRecord only stores coordinates for a single active subtask.
	for _, node := range wfDef.Nodes {
		if node.Type == engine.NodeTypeGateway &&
			(node.GatewayType == engine.GatewayTypeParallelSplit || node.GatewayType == "INCLUSIVE_SPLIT") {
			return nil, fmt.Errorf("parallel subtasks are not supported: task workflow %s contains parallel gateway %s (%s)", wfDef.ID, node.ID, node.GatewayType)
		}
	}

	err := tm.taskWorkflowManager.StartWorkflow(context.Background(), taskWorkflowID, wfDef, initialData)
	if err != nil {
		return nil, fmt.Errorf("failed to start task workflow: %v", err)
	}
	log.Printf("[TaskManager] Started task workflow %s for task %s", taskWorkflowID, taskID)
	return nil, activity.ErrResultPending
}

// StartSubTask is called by the Task's workflow engine when it activates an interaction step.
// It routes to the correct capability handler dynamically from the plugin registry.
func (tm *TaskManager) StartSubTask(payload engine.TaskPayload) (map[string]any, error) {
	record, exists := tm.db.GetTaskByWorkflowID(payload.WorkflowID)
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
	subTemplate, ok := tm.registry.GetSubTaskTemplate(payload.TaskTemplateID)
	if !ok {
		return nil, fmt.Errorf("[StartSubTask] unknown task_template_id: %s", payload.TaskTemplateID)
	}

	// 2. Fetch the plugin from our registry using TaskType
	plugin, ok := tm.pluginsRegistry.Get(subTemplate.TaskType)
	if !ok {
		return nil, fmt.Errorf("[StartSubTask] unregistered plugin for task type %s (required for template: %s)", subTemplate.TaskType, payload.TaskTemplateID)
	}

	// 3. Execute the plugin
	pluginCtx := plugins.PluginContext{
		Context: context.Background(),
		Record:  &record,
		Inputs:  payload.Inputs,
	}

	err := plugin.Execute(pluginCtx, subTemplate.PluginProperties)
	if errors.Is(err, plugins.ErrSuspended) {
		tm.db.SaveTask(record)
		return nil, activity.ErrResultPending
	}
	if err != nil {
		return nil, fmt.Errorf("[StartSubTask] plugin for task type %q execution failed: %w", subTemplate.TaskType, err)
	}

	tm.db.SaveTask(record)

	// Otherwise, this step completed synchronously. Return its modified payload immediately to transition directly.
	return record.Data, nil
}

// HandleTaskCompletion is called when a Task workflow hits its END node.
// It marks the task complete and fires the onTaskCompleted callback to resume the parent workflow.
func (tm *TaskManager) HandleTaskCompletion(workflowID string, finalVariables map[string]any) error {
	record, exists := tm.db.GetTaskByWorkflowID(workflowID)
	if !exists {
		// Not a workflow we own — safe to ignore.
		return nil
	}

	log.Printf("[TaskManager] Task workflow %s completed for task %s", workflowID, record.TaskID)

	record.State = "COMPLETED"
	tm.db.SaveTask(record)

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
	record, exists := tm.db.GetTask(taskID)
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	if record.State == "COMPLETED" {
		return fmt.Errorf("task %s already completed", taskID)
	}

	// Merge submitted data into the stored namespaced Data map
	if record.Data == nil {
		record.Data = make(map[string]any)
	}
	for k, v := range payload {
		record.Data[k] = v
	}
	tm.db.SaveTask(record)

	log.Printf("[TaskManager] Waking active activity %s in workflow %s (task %s)",
		record.SubTaskNodeID, record.TaskWorkflowID, taskID)

	err := tm.taskWorkflowManager.TaskDone(
		ctx,
		record.TaskWorkflowID,
		record.TaskRunID,
		record.SubTaskNodeID,
		record.Data, // pass full namespaced state back to the workflow
	)
	if err != nil {
		return fmt.Errorf("failed to resume task workflow: %w", err)
	}

	return nil
}

// GetTaskRenderInfo retrieves a task record and dynamically decorates it with rich render metadata
// (like JSON schemas) fetched on-the-fly from its executing plugin.
func (tm *TaskManager) GetTaskRenderInfo(taskID string) (TaskView, error) {
	record, exists := tm.db.GetTask(taskID)
	if !exists {
		return TaskView{}, fmt.Errorf("task record %s not found", taskID)
	}

	view, err := tm.renderer.Render(record.RenderConfig, renderer.Facts{State: record.State, Data: record.Data})
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

// GetAllTasksRenderInfo retrieves all tasks in the store and decorates each of them with dynamic render info.
func (tm *TaskManager) GetAllTasksRenderInfo() []TaskView {
	records := tm.db.GetAllTasks()
	resList := make([]TaskView, 0, len(records))
	for _, r := range records {
		info, err := tm.GetTaskRenderInfo(r.TaskID)
		if err != nil {
			slog.Info(fmt.Sprintf("failed to render task %s: %v", r.TaskID, err))
		}
		resList = append(resList, info)
	}
	return resList
}
