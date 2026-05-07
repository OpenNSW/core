package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	engine "github.com/OpenNSW/go-temporal-workflow"
	"github.com/OpenNSW/nsw-task-flow/plugins"
	"github.com/OpenNSW/nsw-task-flow/store"
	"github.com/google/uuid"
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
	registry            *TaskTemplateRegistry
	pluginsRegistry     *plugins.Registry
	onTaskCompleted     TaskCompletedCallback
	taskWorkflowManager engine.TemporalManager
	taskDefPath         string
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
	registry *TaskTemplateRegistry,
	pluginsRegistry *plugins.Registry,
	taskWorkflowManager engine.TemporalManager,
	onTaskCompleted TaskCompletedCallback,
) *TaskManager {
	return &TaskManager{
		db:                  db,
		registry:            registry,
		pluginsRegistry:     pluginsRegistry,
		onTaskCompleted:     onTaskCompleted,
		taskWorkflowManager: taskWorkflowManager,
		taskDefPath:         "task.json",
	}
}

// WithTaskDefPath overrides the path to the Task workflow definition JSON.
// Useful when running tests or running from an alternate directory.
func (tm *TaskManager) WithTaskDefPath(path string) *TaskManager {
	tm.taskDefPath = path
	return tm
}

// StartTask is called by the parent workflow engine when it activates a TASK node.
// It looks up the template registry, creates a single DB record with parent
// coordinates, and kicks off the Task's internal workflow.
func (tm *TaskManager) StartTask(payload engine.TaskPayload) error {
	var def engine.WorkflowDefinition
	var regEntry TaskTemplateEntry

	if workflowDef, exists := tm.registry.GetWorkflow(payload.TaskTemplateID); exists {
		def = workflowDef
		regEntry = TaskTemplateEntry{
			TemplateID: payload.TaskTemplateID,
			TaskType:   "COMPOSITE",
		}
		log.Printf("[TaskManager] Found registered sub-workflow definition for template ID %s", payload.TaskTemplateID)
	} else {
		var ok bool
		regEntry, ok = tm.registry.Get(payload.TaskTemplateID)
		if !ok {
			return fmt.Errorf("unknown task_template_id: %s (neither registered as sub-workflow nor task template)", payload.TaskTemplateID)
		}

		// Fallback to reading the default static task definition file (backward compatibility)
		fileBytes, err := os.ReadFile(tm.taskDefPath)
		if err != nil {
			return fmt.Errorf("failed to read task def file %s: %v", tm.taskDefPath, err)
		}
		if err := json.Unmarshal(fileBytes, &def); err != nil {
			return fmt.Errorf("failed to parse task def file %s: %v", tm.taskDefPath, err)
		}
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
		TaskType:         regEntry.TaskType,
		Status:           "STARTING",
		ParentWorkflowID: payload.WorkflowID,
		ParentRunID:      payload.RunID,
		ParentNodeID:     payload.NodeID,
		TaskWorkflowID:   taskWorkflowID,
		Data:             initialData,
		CreatedAt:        time.Now(),
	}
	tm.db.SaveTask(record)
	log.Printf("[TaskManager] Created Task record %s (template=%s, task_type=%s)", taskID, payload.TaskTemplateID, regEntry.TaskType)

	// Verify that there are no parallel execution paths, as TaskRecord only stores coordinates for a single active subtask.
	for _, node := range def.Nodes {
		if node.Type == engine.NodeTypeGateway &&
			(node.GatewayType == engine.GatewayTypeParallelSplit || node.GatewayType == "INCLUSIVE_SPLIT") {
			return fmt.Errorf("parallel subtasks are not supported: task workflow %s contains parallel gateway %s (%s)", def.ID, node.ID, node.GatewayType)
		}
	}

	err := tm.taskWorkflowManager.StartWorkflow(context.Background(), taskWorkflowID, def, initialData)
	if err != nil {
		return fmt.Errorf("failed to start task workflow: %v", err)
	}
	log.Printf("[TaskManager] Started task workflow %s for task %s", taskWorkflowID, taskID)
	return nil
}

// StartSubTask is called by the Task's workflow engine when it activates an interaction step.
// It routes to the correct capability handler dynamically from the plugin registry.
func (tm *TaskManager) StartSubTask(payload engine.TaskPayload) error {
	record, exists := tm.db.GetTaskByWorkflowID(payload.WorkflowID)
	if !exists {
		return fmt.Errorf("[StartSubTask] no task record found for workflow %s", payload.WorkflowID)
	}

	record.TaskRunID = payload.RunID
	record.SubTaskNodeID = payload.NodeID

	for k, v := range payload.Inputs {
		setNestedKey(record.Data, k, v)
	}

	// 1. Look up the task template to find the associated plugin config
	regEntry, ok := tm.registry.Get(payload.TaskTemplateID)
	if !ok {
		return fmt.Errorf("[StartSubTask] unknown task_template_id: %s", payload.TaskTemplateID)
	}

	// 2. Fetch the plugin from our registry using both TaskType and PluginName
	plugin, ok := tm.pluginsRegistry.Get(regEntry.TaskType, regEntry.PluginName)
	if !ok {
		return fmt.Errorf("[StartSubTask] unregistered plugin: %s for task type %s (required for template: %s)", regEntry.PluginName, regEntry.TaskType, payload.TaskTemplateID)
	}

	// 3. Execute the plugin
	pluginCtx := plugins.PluginContext{
		Context: context.Background(),
		Record:  &record,
		Inputs:  payload.Inputs,
	}

	if err := plugin.Execute(pluginCtx, regEntry.PluginProperties); err != nil {
		return fmt.Errorf("[StartSubTask] plugin %q execution failed: %w", regEntry.PluginName, err)
	}

	tm.db.SaveTask(record)
	return nil
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

	record.Status = "COMPLETED"
	tm.db.SaveTask(record)

	err := tm.onTaskCompleted(record.ParentWorkflowID, record.ParentRunID, record.ParentNodeID, finalVariables)
	if err != nil {
		log.Printf("[TaskManager] Failed to execute task completion callback for %s: %v", record.TaskID, err)
		return err
	}

	log.Printf("[TaskManager] Successfully processed completion for task %s", record.TaskID)
	return nil
}

// GetTask retrieves a single task record by its ID.
func (tm *TaskManager) GetTask(taskID string) (store.TaskRecord, bool) {
	return tm.db.GetTask(taskID)
}

// GetAllTasks retrieves all task records in the store.
func (tm *TaskManager) GetAllTasks() []store.TaskRecord {
	return tm.db.GetAllTasks()
}

// CompleteTaskStep is the public API for external clients or portals to submit form/interaction
// data and resume the active step in the corresponding Task workflow.
func (tm *TaskManager) CompleteTaskStep(ctx context.Context, taskID string, payload map[string]any) error {
	record, exists := tm.db.GetTask(taskID)
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	if record.Status == "COMPLETED" {
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

// GetDB returns the underlying task store.
func (tm *TaskManager) GetDB() store.TaskStore {
	return tm.db
}

// GetTaskWorkflowManager returns the Task's TemporalManager.
func (tm *TaskManager) GetTaskWorkflowManager() engine.TemporalManager {
	return tm.taskWorkflowManager
}

// GetPluginsRegistry returns the task execution plugins registry.
func (tm *TaskManager) GetPluginsRegistry() *plugins.Registry {
	return tm.pluginsRegistry
}

// setNestedKey sets a value in a map using a dot-separated path.
func setNestedKey(m map[string]any, dotPath string, value any) {
	if dotPath == "" {
		return
	}
	for i := 0; i < len(dotPath); i++ {
		if dotPath[i] == '.' {
			key := dotPath[:i]
			rest := dotPath[i+1:]
			sub, ok := m[key]
			if !ok || sub == nil {
				sub = make(map[string]any)
			}
			subMap, ok := sub.(map[string]any)
			if !ok {
				subMap = make(map[string]any)
			}
			setNestedKey(subMap, rest, value)
			m[key] = subMap
			return
		}
	}
	m[dotPath] = value
}
