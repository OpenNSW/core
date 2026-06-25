// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifactadapter/generictemplate"
	"github.com/OpenNSW/core/artifactadapter/subtasktemplate"
	"github.com/OpenNSW/core/artifactadapter/tasktemplate"
	"github.com/OpenNSW/core/artifactadapter/types"
	"github.com/OpenNSW/core/artifactadapter/workflowdef"
	"github.com/OpenNSW/core/internal/deepcopy"
	"github.com/OpenNSW/core/internal/maputil"
	"github.com/OpenNSW/core/taskflow/extensions"
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
	extensionsRegistry  *extensions.Registry
	onTaskCompleted     TaskCompletedCallback
	taskWorkflowManager engine.TemporalManager
	logger              *slog.Logger
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
	extensionsRegistry *extensions.Registry,
	taskWorkflowManager engine.TemporalManager,
	onTaskCompleted TaskCompletedCallback,
	renderer renderer.Renderer,
	opts ...Option,
) *TaskManager {
	tm := &TaskManager{
		db:                  db,
		registry:            registry,
		pluginsRegistry:     pluginsRegistry,
		extensionsRegistry:  extensionsRegistry,
		onTaskCompleted:     onTaskCompleted,
		taskWorkflowManager: taskWorkflowManager,
		renderer:            renderer,
		logger:              slog.Default(),
	}
	for _, opt := range opts {
		opt(tm)
	}
	if tm.logger == nil {
		tm.logger = slog.Default()
	}
	return tm
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
		maputil.SetNestedKey(initialData, k, v)
	}
	initialData["_task_id"] = taskID

	// root_workflow_id is the top-level consignment ID — the first segment of
	// parent_workflow_id before any "--" separator introduced by SPLIT_TASK
	// child workflow IDs (format: "{root}--{nodeID}--{branchID}").
	rootWorkflowID := payload.WorkflowID
	if idx := strings.Index(payload.WorkflowID, "--"); idx != -1 {
		rootWorkflowID = payload.WorkflowID[:idx]
	}

	record := store.TaskRecord{
		TaskID:           taskID,
		TaskType:         template.Type,
		State:            "STARTING",
		RenderConfig:     renderConfig,
		ParentWorkflowID: payload.WorkflowID,
		ParentRunID:      payload.RunID,
		ParentNodeID:     payload.NodeID,
		RootWorkflowID:   rootWorkflowID,
		TaskWorkflowID:   taskWorkflowID,
		Data:             initialData,
		CreatedAt:        time.Now(),
	}
	// Verify that there are no parallel execution paths before writing anything,
	// as TaskRecord only stores coordinates for a single active subtask.
	for _, node := range wfDef.Nodes {
		if node.Type == engine.NodeTypeGateway && node.GatewayType == engine.GatewayTypeParallelSplit {
			return nil, fmt.Errorf("parallel subtasks are not supported: task workflow %s contains parallel gateway %s (%s)", wfDef.ID, node.ID, node.GatewayType)
		}
	}

	tm.db.SaveTask(ctx, record)
	tm.logger.InfoContext(ctx, "task record created", "task_id", taskID, "template", payload.TaskTemplateID, "task_type", template.Type)

	err = tm.taskWorkflowManager.StartWorkflow(ctx, taskWorkflowID, wfDef, initialData)
	if err != nil {
		return nil, fmt.Errorf("failed to start task workflow: %v", err)
	}
	tm.logger.InfoContext(ctx, "task workflow started", "task_workflow_id", taskWorkflowID, "task_id", taskID)
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
		maputil.SetNestedKey(record.Data, k, v)
	}

	subTemplate, err := subtasktemplate.Load(ctx, tm.registry, payload.TaskTemplateID)
	if err != nil {
		return nil, fmt.Errorf("[StartSubTask] load subtask template %q: %w", payload.TaskTemplateID, err)
	}

	// 2. Fetch the plugin from our registry using TaskType
	plugin, ok := tm.pluginsRegistry.Get(subTemplate.TaskType)
	if !ok {
		return nil, fmt.Errorf("[StartSubTask] unregistered plugin for task type %s (required for template: %s)", subTemplate.TaskType, payload.TaskTemplateID)
	}

	// 3. Execute the plugin
	pluginCtx := plugins.PluginContext{
		Context:         ctx,
		Record:          &record,
		Inputs:          payload.Inputs,
		OutputNamespace: subTemplate.OutputNamespace,
	}

	tm.logger.InfoContext(ctx, "starting subtask", "task_id", record.TaskID, "subtask_node_id", record.SubTaskNodeID, "task_type", subTemplate.TaskType, "template", payload.TaskTemplateID)

	err = plugin.Execute(pluginCtx, subTemplate.PluginProperties)
	if errors.Is(err, plugins.ErrSuspended) {
		tm.db.SaveTask(pluginCtx.Context, record)
		tm.logger.InfoContext(ctx, "subtask suspended, awaiting async completion", "task_id", record.TaskID, "subtask_node_id", record.SubTaskNodeID, "task_type", subTemplate.TaskType)
		return nil, activity.ErrResultPending
	}
	if err != nil {
		return nil, fmt.Errorf("[StartSubTask] plugin for task type %q execution failed: %w", subTemplate.TaskType, err)
	}

	tm.db.SaveTask(pluginCtx.Context, record)

	// Otherwise, this step completed synchronously. Return its modified payload immediately to transition directly.
	tm.logger.InfoContext(ctx, "subtask completed synchronously", "task_id", record.TaskID, "subtask_node_id", record.SubTaskNodeID, "task_type", subTemplate.TaskType)
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

	tm.logger.InfoContext(ctx, "task workflow completed", "task_workflow_id", workflowID, "task_id", record.TaskID)

	// Idempotency guard: Temporal may retry this activity; skip if already completed.
	if record.State == "COMPLETED" {
		return nil
	}

	err := tm.onTaskCompleted(record.ParentWorkflowID, record.ParentRunID, record.ParentNodeID, finalVariables)
	if err != nil {
		tm.logger.ErrorContext(ctx, "task completion callback failed", "task_id", record.TaskID, "error", err)
		return err
	}

	tm.logger.InfoContext(ctx, "task completion processed", "task_id", record.TaskID)
	record.State = "COMPLETED"
	tm.db.SaveTask(ctx, record)

	return nil
}

// waitForActiveSubtask polls the store for a short period to see if the task moves
// out of the STARTING state and registers an active subtask. This handles the race
// condition where an external callback or webhook completes a step immediately after
// task creation, before the Temporal worker has executed the first StartSubTask activity.
func (tm *TaskManager) waitForActiveSubtask(ctx context.Context, taskID string) (store.TaskRecord, error) {
	const (
		maxAttempts = 5
		delay       = 200 * time.Millisecond
	)
	for i := 0; i < maxAttempts; i++ {
		record, exists := tm.db.GetTask(ctx, taskID)
		if !exists {
			return store.TaskRecord{}, fmt.Errorf("task %s not found", taskID)
		}
		if record.ActiveTaskTemplateID != "" || record.State == "COMPLETED" {
			return record, nil
		}
		if i == maxAttempts-1 {
			return record, nil
		}
		select {
		case <-ctx.Done():
			return store.TaskRecord{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	return store.TaskRecord{}, nil
}

// CompleteTaskStep is the public API for external clients or portals to submit form/interaction
// data and resume the active step in the corresponding Task workflow.
func (tm *TaskManager) CompleteTaskStep(ctx context.Context, taskID string, payload map[string]any) error {
	record, err := tm.waitForActiveSubtask(ctx, taskID)
	if err != nil {
		return err
	}

	if record.State == "COMPLETED" {
		return fmt.Errorf("task %s already completed", taskID)
	}

	if record.ActiveTaskTemplateID == "" {
		return fmt.Errorf("task %s has no active subtask step to complete", taskID)
	}

	subTemplate, err := subtasktemplate.Load(ctx, tm.registry, record.ActiveTaskTemplateID)
	if err != nil {
		return fmt.Errorf("failed to load active subtask template %q: %w", record.ActiveTaskTemplateID, err)
	}

	if record.Data == nil {
		record.Data = make(map[string]any)
	}

	// 1. Run PRE_RESUME Extensions (Blocking, Read-only)
	// Extensions receive deep copies of the record and payload so they can
	// validate/inspect them but cannot mutate the data that gets persisted or
	// sent to the workflow.
	preResumeRecord := record.DeepCopy()
	if err := tm.runExtensions(ctx, &preResumeRecord, types.PhasePreResume, subTemplate.Extensions, deepcopy.Map(payload), true); err != nil {
		return err
	}

	// Writes are confined to the active subtask's declared OutputNamespace,
	// which was loaded from the registry. An open
	// top-level merge would let callers overwrite slots owned by other
	// subtasks (or internal keys like _task_id), so the namespace is
	// required for any non-empty payload. If it's missing we log loudly and
	// drop the payload — the workflow still resumes so a misconfigured
	// template doesn't break a running task.
	if len(payload) > 0 {
		if subTemplate.OutputNamespace == "" {
			tm.logger.WarnContext(ctx, "dropping submission payload: active subtask declares no output_namespace", "task_id", taskID, "template", record.ActiveTaskTemplateID, "dropped_keys", payloadKeys(payload))
		} else {
			// System variables (keys prefixed with "__") are runtime-internal
			// and must not be persisted to the output namespace; they are still
			// passed back to the workflow below.
			record.Data[subTemplate.OutputNamespace] = withoutSystemVars(payload)
		}
	}
	tm.db.SaveTask(ctx, record)

	tm.logger.InfoContext(ctx, "waking active activity", "activity_id", record.SubTaskNodeID, "task_workflow_id", record.TaskWorkflowID, "task_id", taskID)

	err = tm.taskWorkflowManager.TaskDone(
		ctx,
		record.TaskWorkflowID,
		record.TaskRunID,
		record.SubTaskNodeID,
		payload, // pass full namespaced state back to the workflow
	)
	if err != nil {
		return fmt.Errorf("failed to resume task workflow: %w", err)
	}

	// 2. Run POST_RESUME Extensions (Non-Blocking, Immutable, Async)
	if tm.extensionsRegistry != nil && len(subTemplate.Extensions) > 0 {
		// Deep copy the payload and record so extensions cannot mutate the data
		// that was persisted/sent to the workflow (the read-only contract). As a
		// bonus this keeps the async goroutine from sharing nested maps/slices
		// with the live data, avoiding concurrent-map data races.
		copiedPayload := deepcopy.Map(payload)
		copiedRecord := record.DeepCopy()

		// Use context.WithoutCancel to propagate tracing/telemetry context without cancellation
		bgCtx := context.WithoutCancel(ctx)

		// Execute in background; errors are logged inside runExtensions, not returned to client.
		go func() {
			_ = tm.runExtensions(bgCtx, &copiedRecord, types.PhasePostResume, subTemplate.Extensions, copiedPayload, false)
		}()
	}

	return nil
}

// runExtensions executes the configured extensions matching phase against the
// record. When stopOnError is true (pre-resume), the first failure aborts and is
// returned; otherwise (post-resume) failures are logged and execution continues.
func (tm *TaskManager) runExtensions(ctx context.Context, record *store.TaskRecord, phase types.ExecutionPhase, extensions []types.ExtensionConfig, payload map[string]any, stopOnError bool) error {
	if tm.extensionsRegistry == nil {
		return nil
	}
	for _, extCfg := range extensions {
		if extCfg.Phase != phase {
			continue
		}
		ext, registered := tm.extensionsRegistry.Get(extCfg.ID)
		if !registered {
			err := fmt.Errorf("%s extension %q configured but not registered", phase, extCfg.ID)
			if stopOnError {
				return err
			}
			tm.logger.ErrorContext(ctx, "extension not registered", "phase", phase, "extension_id", extCfg.ID)
			continue
		}
		if err := ext.Execute(ctx, record, payload, extCfg.Properties); err != nil {
			err = fmt.Errorf("%s extension %q failed: %w", phase, extCfg.ID, err)
			if stopOnError {
				return err
			}
			tm.logger.ErrorContext(ctx, "extension execution failed", "phase", phase, "extension_id", extCfg.ID, "error", err)
		}
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

// withoutSystemVars returns a copy of payload with system variables removed.
// System variables are keys prefixed with "__" (double underscore); they are
// runtime-internal and should not be written to a subtask's output namespace.
func withoutSystemVars(payload map[string]any) map[string]any {
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		if strings.HasPrefix(k, "__") {
			continue
		}
		out[k] = v
	}
	return out
}

func payloadKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	return keys
}
