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
	"github.com/OpenNSW/core/artifact/adapter/generictemplate"
	"github.com/OpenNSW/core/artifact/adapter/steptemplate"
	"github.com/OpenNSW/core/artifact/adapter/tasktemplate"
	"github.com/OpenNSW/core/artifact/adapter/types"
	"github.com/OpenNSW/core/artifact/adapter/workflowdef"
	"github.com/OpenNSW/core/shared/deepcopy"
	"github.com/OpenNSW/core/shared/maputil"
	"github.com/OpenNSW/core/taskflow/extensions"
	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/renderer"
	"github.com/OpenNSW/core/taskflow/store"
	engine "github.com/OpenNSW/core/workflow"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
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

3. Step (Interaction Steps):
   Individual, potentially asynchronous execution nodes inside the Task (e.g., waiting for
   a user form submission, or queuing a request in an external agency portal). These are
   dispatched via StartTaskStep() and resumed via CompleteTaskStep().

Flow Diagram:
              [Parent Workflow]
                     │
                     ▼ (StartTask)
              [TaskManager] ────► [Task Record created in DB]
                     │
                     ▼ (StartTaskWorkflow)
              [Task Workflow]
                     │
                     ▼ (StartTaskStep)
              [Step Node] (e.g., PENDING_USER status)
                     │
                     ▼ (CompleteTaskStep)
           [Resume Step & Continue]
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

// ErrStaleStep is returned (wrapped in a non-retryable Temporal error) by StartTaskStep when a later
// step has already started, so this attempt is a late or duplicate one that must do nothing.
var ErrStaleStep = errors.New("step is no longer the active step of the task")

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
//   - registry            — artifact registry holding task templates, step templates, workflow definitions, and render configs.
//   - pluginsRegistry     — registry containing task execution plugin handlers.
//   - taskWorkflowManager — the TemporalManager used to start and complete Task sub-workflows.
//   - onTaskCompleted     — callback invoked when a Task workflow finishes;
//     typically invokes Parent.CompleteActivation to resume the parent workflow using stored coordinates.
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

	// Use the parent's step ID as the TaskID. It names this run of the parent's TASK node, so it is
	// globally unique and a node revisited by a loop starts a new task rather than reusing the old
	// one. It is also a UUID.
	taskID := payload.ActivationID
	if taskID == "" {
		return nil, fmt.Errorf("start task from template %q: payload has no step ID", payload.TaskTemplateID)
	}
	taskWorkflowID := "task-wf-" + taskID

	initialData := make(map[string]any)
	for k, v := range payload.Inputs {
		maputil.SetNestedKey(initialData, k, v)
	}
	initialData["_task_id"] = taskID

	// root_workflow_id is the top-level workflow's ID, propagated by the engine through every
	// level of nesting (see engine.VarRootWorkflowID).
	rootWorkflowID := payload.RootWorkflowID

	record := store.TaskRecord{
		TaskID:           taskID,
		TaskType:         template.Type,
		State:            "STARTING",
		RenderConfig:     renderConfig,
		ParentWorkflowID: payload.WorkflowID,
		ParentRunID:      payload.RunID,
		ParentStepID:     payload.ActivationID, // the parent's Activity to complete: its step ID
		RootWorkflowID:   rootWorkflowID,
		TaskWorkflowID:   taskWorkflowID,
		Data:             initialData,
		CreatedAt:        time.Now(),
	}
	// Verify that there are no parallel execution paths before writing anything,
	// as TaskRecord only stores coordinates for a single active step.
	for _, node := range wfDef.Nodes {
		if node.Type == engine.NodeTypeGateway && node.GatewayType == engine.GatewayTypeParallelSplit {
			return nil, fmt.Errorf("parallel steps are not supported: task workflow %s contains parallel gateway %s (%s)", wfDef.ID, node.ID, node.GatewayType)
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

// StartTaskStep is called by the Task's workflow engine when it activates an interaction step.
// It routes to the correct capability handler dynamically from the plugin registry.
//
// The step is claimed in the DB before the plugin runs, and the plugin's render state is written
// only if the step is still the active one:
//
//  1. Claim: make this step (payload.ActivationID) the task's active step, at payload.Seq. The claim
//     matches only if no later step has started. A retry of this same step matches again. A late
//     attempt for a step that is already over matches nothing, and returns ErrStaleStep before the
//     plugin runs, so it makes no outbound call and writes nothing.
//  2. The plugin runs on an in-memory copy of the record.
//  3. Render write: store the plugin's state and data, guarded by the active step and seq. A fast
//     callback can already have completed this step and started the next one, in which case the
//     write matches nothing and is dropped. That is success, not an error.
func (tm *TaskManager) StartTaskStep(ctx context.Context, payload engine.TaskPayload) (map[string]any, error) {
	if payload.ActivationID == "" {
		return nil, fmt.Errorf("[StartTaskStep] payload for workflow %s has no step ID", payload.WorkflowID)
	}

	record, exists := tm.db.GetTaskByWorkflowID(ctx, payload.WorkflowID)
	if !exists {
		return nil, fmt.Errorf("[StartTaskStep] no task record found for workflow %s", payload.WorkflowID)
	}

	stepTemplate, err := steptemplate.Load(ctx, tm.registry, payload.TaskTemplateID)
	if err != nil {
		return nil, fmt.Errorf("[StartTaskStep] load step template %q: %w", payload.TaskTemplateID, err)
	}

	// Fetch the plugin from our registry using PluginType
	plugin, ok := tm.pluginsRegistry.Get(stepTemplate.PluginType)
	if !ok {
		return nil, fmt.Errorf("[StartTaskStep] unregistered plugin for plugin type %s (required for template: %s)", stepTemplate.PluginType, payload.TaskTemplateID)
	}

	// The step's data is exactly its mapped inputs: everything an earlier step submitted reaches
	// this one through the workflow's variables and the node's input mapping, not through the row.
	data := make(map[string]any, len(payload.Inputs))
	for k, v := range payload.Inputs {
		maputil.SetNestedKey(data, k, v)
	}

	rows, err := tm.db.ClaimStep(ctx, record.TaskID, store.StepClaim{
		StepID:               payload.ActivationID,
		Seq:                  payload.Seq,
		ActiveTaskTemplateID: payload.TaskTemplateID,
		State:                store.StateStartingStep,
		Data:                 data,
	})
	if err != nil {
		return nil, fmt.Errorf("[StartTaskStep] claim step %s of task %s: %w", payload.ActivationID, record.TaskID, err)
	}
	if rows == 0 {
		tm.logger.InfoContext(ctx, "dropping stale start of step", "task_id", record.TaskID, "step_id", payload.ActivationID, "seq", payload.Seq)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("step %s (seq %d) of task %s is no longer current", payload.ActivationID, payload.Seq, record.TaskID),
			"StaleStep", ErrStaleStep)
	}

	// The record the plugin sees is what the claim just stored.
	record.ActiveStepID = payload.ActivationID
	record.Seq = payload.Seq
	record.ActiveTaskTemplateID = payload.TaskTemplateID
	record.State = store.StateStartingStep
	record.Data = data

	pluginCtx := plugins.PluginContext{
		Context:         ctx,
		Record:          &record,
		Inputs:          payload.Inputs,
		OutputNamespace: stepTemplate.OutputNamespace,
	}

	tm.logger.InfoContext(ctx, "starting step", "task_id", record.TaskID, "step_id", payload.ActivationID, "seq", payload.Seq, "plugin_type", stepTemplate.PluginType, "template", payload.TaskTemplateID)

	err = plugin.Execute(pluginCtx, stepTemplate.PluginProperties)
	suspended := errors.Is(err, plugins.ErrSuspended)
	if err != nil && !suspended {
		return nil, fmt.Errorf("[StartTaskStep] plugin for plugin type %q execution failed: %w", stepTemplate.PluginType, err)
	}

	rows, err = tm.db.WriteRenderState(pluginCtx.Context, record.TaskID, payload.ActivationID, payload.Seq, record.State, record.Data)
	if err != nil {
		return nil, fmt.Errorf("[StartTaskStep] write render state of step %s of task %s: %w", payload.ActivationID, record.TaskID, err)
	}
	if rows == 0 {
		tm.logger.InfoContext(ctx, "dropped render state of a step that is no longer current", "task_id", record.TaskID, "step_id", payload.ActivationID, "seq", payload.Seq)
	}

	if suspended {
		tm.logger.InfoContext(ctx, "step suspended, awaiting async completion", "task_id", record.TaskID, "step_id", payload.ActivationID, "plugin_type", stepTemplate.PluginType)
		return nil, activity.ErrResultPending
	}

	// Otherwise, this step completed synchronously. Return its modified payload immediately to transition directly.
	tm.logger.InfoContext(ctx, "step completed synchronously", "task_id", record.TaskID, "step_id", payload.ActivationID, "plugin_type", stepTemplate.PluginType)
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

	err := tm.onTaskCompleted(record.ParentWorkflowID, record.ParentRunID, record.ParentStepID, finalVariables)
	if err != nil {
		tm.logger.ErrorContext(ctx, "task completion callback failed", "task_id", record.TaskID, "error", err)
		return err
	}

	record.State = "COMPLETED"
	tm.db.SaveTask(ctx, record)

	return nil
}

// CompleteTaskStep is the public API for external clients or portals to submit form/interaction
// data and resume the active step in the corresponding Task workflow.
func (tm *TaskManager) CompleteTaskStep(ctx context.Context, taskID string, payload map[string]any) error {
	record, exists := tm.db.GetTask(ctx, taskID)
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	if record.State == store.StateCompleted {
		return fmt.Errorf("task %s already completed", taskID)
	}

	// No step is active until the first StartTaskStep has claimed it. Nothing addresses a step before
	// then: a caller learns a step exists only from the task view or from a dispatch, and both
	// come after the claim.
	if record.ActiveStepID == "" || record.ActiveTaskTemplateID == "" {
		return fmt.Errorf("task %s has no active step to complete", taskID)
	}

	stepTemplate, err := steptemplate.Load(ctx, tm.registry, record.ActiveTaskTemplateID)
	if err != nil {
		return fmt.Errorf("failed to load active step template %q: %w", record.ActiveTaskTemplateID, err)
	}

	if record.Data == nil {
		record.Data = make(map[string]any)
	}

	// 1. Run PRE_RESUME Extensions (Blocking, Read-only)
	// Extensions receive deep copies of the record and payload so they can
	// validate/inspect them but cannot mutate the data that gets persisted or
	// sent to the workflow.
	preResumeRecord := record.DeepCopy()
	if err := tm.runExtensions(ctx, &preResumeRecord, types.PhasePreResume, stepTemplate.Extensions, deepcopy.Map(payload), true); err != nil {
		return err
	}

	// Writes are confined to the active step's declared OutputNamespace,
	// which was loaded from the registry. An open
	// top-level merge would let callers overwrite slots owned by other
	// steps (or internal keys like _task_id), so the namespace is
	// required for any non-empty payload. If it's missing we log loudly and
	// drop the payload — the workflow still resumes so a misconfigured
	// template doesn't break a running task.
	if len(payload) > 0 {
		if stepTemplate.OutputNamespace == "" {
			tm.logger.WarnContext(ctx, "dropping submission payload: active step declares no output_namespace", "task_id", taskID, "template", record.ActiveTaskTemplateID, "dropped_keys", payloadKeys(payload))
		} else {
			// System variables (keys prefixed with "__") are runtime-internal
			// and must not be persisted to the output namespace; they are still
			// passed back to the workflow below.
			record.Data[stepTemplate.OutputNamespace] = withoutSystemVars(payload)
		}
	}
	tm.logger.InfoContext(ctx, "waking active activity", "step_id", record.ActiveStepID, "task_workflow_id", record.TaskWorkflowID, "task_id", taskID)

	// CompleteActivation is intentionally called before SaveTask. Temporal enforces
	// exactly-once completion per activity (CompleteActivityByID fails for
	// any caller after the first), so on a duplicate/racing CompleteTaskStep
	// call for the same step, only the winner reaches SaveTask below — the
	// loser returns here without persisting its (possibly stale) Data. Do not
	// reorder this without re-adding an equivalent guard: swapping it back
	// re-opens a lost-update race where the loser's write can land after the
	// winner's and silently overwrite it.
	err = tm.taskWorkflowManager.CompleteActivation(
		ctx,
		record.TaskWorkflowID,
		"", // the workflow's current run: a step ID is unique within the workflow
		record.ActiveStepID,
		payload, // pass full namespaced state back to the workflow
	)
	if err != nil {
		return fmt.Errorf("failed to resume task workflow: %w", err)
	}

	tm.db.SaveTask(ctx, record)

	// 2. Run POST_RESUME Extensions (Non-Blocking, Immutable, Async)
	if tm.extensionsRegistry != nil && len(stepTemplate.Extensions) > 0 {
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
			_ = tm.runExtensions(bgCtx, &copiedRecord, types.PhasePostResume, stepTemplate.Extensions, copiedPayload, false)
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
// runtime-internal and should not be written to a step's output namespace.
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
