// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// ErrWorkflowNotFound is returned by Manager.GetStatus when the backing engine has no queryable
// execution for the given ID. This covers an ID that was never used, but also one whose execution
// is no longer queryable for any other reason (e.g. its history has aged out of the engine's
// retention) — a Manager implementation cannot always tell the two apart. Callers should check
// for ErrWorkflowNotFound with errors.Is rather than inspecting the underlying runtime's error
// types, so they stay agnostic to whatever engine backs the Manager.
var ErrWorkflowNotFound = errors.New("workflow execution not found")

// ExecutionStatus defines the allowed states for a workflow instance.
type ExecutionStatus string

// Execution status constants for workflows.
const (
	StatusRunning   ExecutionStatus = "RUNNING"
	StatusCompleted ExecutionStatus = "COMPLETED"
	StatusFailed    ExecutionStatus = "FAILED"
)

// TaskPayload represents the contextual data sent to the task executor
// when the workflow engine reaches a "Task" node. It contains the necessary coordinates
// for the task executor to identify the work and eventually report back.
type TaskPayload struct {
	// WorkflowID is the unique identifier for the overall business process instance.
	WorkflowID string
	// RunID is the unique identifier for this specific execution attempt.
	RunID string
	// NodeID is the ID of the graph node currently being executed. This should be
	// unique for each node in the workflow.
	NodeID string
	// TaskTemplateID identifies the specific type of external work/script the task executor should run.
	TaskTemplateID string
	// Inputs contains the specific subset of WorkflowVariables mapped to this task's requirements.
	Inputs map[string]any
	// RootWorkflowID is the workflow ID of the top-level execution. WorkflowID alone doesn't
	// identify the root for a deeply nested child, so the engine propagates this explicitly from
	// the root through every level of nesting (see VarRootWorkflowID).
	RootWorkflowID string
}

// AdminParkPayload carries the context of a node parking in NodeStatusAwaitingAdmin, delivered
// to an AdminParkHandler so the host application can decide how to surface the event — log it,
// emit a metric, persist it, page someone, etc. The engine itself takes no position on that; see
// AdminParkHandler.
type AdminParkPayload struct {
	// WorkflowID is the unique identifier for the workflow execution that parked.
	WorkflowID string
	// RunID is the unique identifier for this specific execution attempt.
	RunID string
	// RootWorkflowID is the workflow ID of the top-level execution (see TaskPayload.RootWorkflowID).
	RootWorkflowID string
	// NodeID is the ID of the graph node that parked.
	NodeID string
	// NodeType is the type of the node that parked (TASK, GATEWAY, etc.).
	NodeType string
	// TaskTemplateID identifies the task template the parked node was running. Empty for
	// non-TASK nodes (e.g. a GATEWAY parked on "no matching conditions").
	TaskTemplateID string
	// Cause is the error message that caused the node to park (NodeInfo.LastError).
	Cause string
	// ParkCategory says why the node parked (see NodeInfo.ParkCategory).
	ParkCategory ParkCategory
	// InputMapping and OutputMapping are the node's mappings from its definition, carried over
	// from NodeInfo.InputMapping/OutputMapping so a handler can report which workflow variable a
	// RETRY reads and which a COMPLETE patch should write, without querying the workflow itself.
	InputMapping  map[string]string
	OutputMapping map[string]string
	// CachedTaskResult holds the Activity's raw result if it had already completed before the
	// node parked (see NodeInfo.CachedTaskResult) — nil if execution never reached that point.
	CachedTaskResult map[string]any
}

// NodeStatus represents the status of a specific workflow node.
type NodeStatus string

// Node status constants.
const (
	NodeStatusNotStarted    NodeStatus = "NOT_STARTED"
	NodeStatusRunning       NodeStatus = "RUNNING"
	NodeStatusCompleted     NodeStatus = "COMPLETED"
	NodeStatusFailed        NodeStatus = "FAILED"
	NodeStatusAwaitingAdmin NodeStatus = "AWAITING_ADMIN"
)

// NodeInfo holds information about the state of one of the nodes in the workflow.
type NodeInfo struct {
	ID             string      `json:"id"`
	CreatedAt      time.Time   `json:"createdAt"`                  // Timestamp of node creation
	UpdatedAt      time.Time   `json:"updatedAt"`                  // Timestamp of last node update
	Type           NodeType    `json:"type"`                       // Type of the node.
	GatewayType    GatewayType `json:"gateway_type,omitempty"`     // See Gateway Types constants
	TaskTemplateID string      `json:"task_template_id,omitempty"` // Identifier for the task template to run
	Status         NodeStatus  `json:"status"`                     // Status of the node

	// TODO: LastError, ParkCategory, InputMapping, OutputMapping and CachedTaskResult are all
	// context for a parked node. Group them into one nested struct (e.g. NodeInfo.Park) instead of
	// growing NodeInfo with flat fields.

	// LastError holds the error that caused the node to enter NodeStatusAwaitingAdmin
	// (or the terminal error if it was ultimately aborted). Cleared on successful resolution.
	LastError string `json:"last_error,omitempty"`
	// ParkCategory says why the node is parked. Set together with LastError; cleared on resolution.
	ParkCategory ParkCategory `json:"park_category,omitempty"`
	// InputMapping and OutputMapping are the node's mappings from its definition (workflow variable
	// -> task input, task result -> workflow variable), so an admin can see which variables a
	// RETRY reads and which a COMPLETE should write. Set together with LastError, only while the
	// node is parked (not on every node); cleared on resolution.
	InputMapping  map[string]string `json:"input_mapping,omitempty"`
	OutputMapping map[string]string `json:"output_mapping,omitempty"`
	// CachedTaskResult holds the most recent raw Activity result for a TASK node, set right
	// after the Activity succeeds and cleared once the node fully completes. It is purely
	// informational: if a node parks with this populated, the Activity has already run, so
	// an admin should prefer AdminActionComplete over AdminActionRetry to avoid re-running it.
	CachedTaskResult map[string]any `json:"cached_task_result,omitempty"`

	// ChildWorkflowIDs lists the workflow IDs of any child GraphInterpreterWorkflow executions
	// spawned by this node (SPLIT_TASK, BATCH_SPLIT, or PARALLEL_SPLIT). It is set once, at
	// spawn time, and is not derived from the children's current status — so an ID remains
	// listed here even after that child workflow completes. A caller can query each ID with the
	// same GetStatus query used on this workflow to walk the execution tree to arbitrary depth.
	ChildWorkflowIDs []string `json:"child_workflow_ids,omitempty"`
}

// WorkflowInstance holds the dynamic runtime state of the workflow execution.
// This struct is returned by the GetStatus query and represents a deterministic
// snapshot of the engine's memory at a given point in time.
type WorkflowInstance struct {
	// ID is the unique ID for this instance of the workflow.
	ID string `json:"id"`
	// Status represents the current execution state.
	Status ExecutionStatus `json:"status"`
	// WorkflowVariables holds the shared, dynamic business data passed between nodes.
	WorkflowVariables map[string]any `json:"workflow_variables"`
	// AuditTrail is a chronologically ordered log of events, milestones, or external signals.
	AuditTrail []string             `json:"audit_trail"`
	NodeInfo   map[string]*NodeInfo `json:"node_states"`
	// Edges contains the workflow graph connections from the workflow definition.
	Edges []Edge `json:"edges"`
}

// UpdateEvent allows the task executor to send asynchronous signals
// into a running workflow (e.g., while a task is active).
type UpdateEvent struct {
	// EventType categorizes the signal (e.g., "AUDIT", "PROGRESS_UPDATE", "UI_HINT")
	// so the workflow knows how to route the data internally.
	EventType string `json:"eventType"`
	// NodeID is the ID of the graph node being updated.
	NodeID string `json:"nodeID"`
	// Payload contains the contextual data for the event (e.g., percentage complete, or audit text).
	Payload map[string]any `json:"payload,omitempty"`
}

// TaskActivationHandler is invoked by the engine whenever the workflow reaches a "Task" node.
// The handler must support two execution paths:
//
// 1. Synchronous Execution:
//   - If the work completes immediately, return a nil error and a map containing the output variables.
//   - The workflow immediately consumes these outputs and proceeds to the next node.
//
// 2. Asynchronous Execution:
//   - If the work is long-running (e.g. awaits external API callback, human UI interaction, etc.),
//     return a nil map and an ErrResultPending error.
//   - The workflow activity pauses and awaits completion. The host application must eventually resume
//     it by calling Manager.TaskDone() with the matching workflow, run, and node IDs.
type TaskActivationHandler func(payload TaskPayload) (map[string]any, error)

// WorkflowCompletionHandler is invoked when the generic DAG workflow successfully reaches an "End" node,
// providing the final, accumulated state of the workflow variables.
type WorkflowCompletionHandler func(workflowID string, finalWorkflowVariables map[string]any) error

// AdminParkHandler is invoked once every time a node parks in NodeStatusAwaitingAdmin —
// including a re-park after a failed AdminActionRetry, since that's newly actionable
// information. It hands the host application full control over how to surface the event (log,
// metric, DB write, page, etc.); the engine itself does not prescribe a mechanism. Registering
// one is optional — a nil handler (the default) means the engine takes no notification action
// at all, and parking still proceeds correctly either way. A handler error is recorded on the
// workflow's own AuditTrail but never blocks the park or the admin resolution flow.
type AdminParkHandler func(AdminParkPayload) error

// Manager acts as the bridge between the external host application and the underlying
// execution engine. It handles workflow lifecycles, external task routing,
// and state queries.
type Manager interface {
	// StartWorkflow starts a workflow using the provided ID.
	// The WorkflowDefinition defines the structure of the workflow graph (nodes and edges).
	// initialWorkflowVariables sets the starting state for the graph's
	// data payload. Returns an error if submission fails.
	StartWorkflow(ctx context.Context, ID string, def WorkflowDefinition, initialWorkflowVariables map[string]any) error

	// TaskDone is called by the external system to resume a paused workflow node.
	// It routes the output data back into the specific workflow's WorkflowVariables using the provided
	// IDs (workflowID, runID, nodeID) that were originally emitted via the TaskActivationHandler.
	TaskDone(ctx context.Context, workflowID, runID, nodeID string, output map[string]any) error

	// TaskUpdate is used to send an update about the task to the workflow.
	// This is typically used to append messages to the workflow's internal state or update
	// UI with hints, progress updates, or audit trail messages.
	// It does not advance the graph's execution state.
	TaskUpdate(ctx context.Context, workflowID, runID string, update UpdateEvent) error

	// GetStatus retrieves a running workflow's in-memory state (the WorkflowInstance), including
	// current variables, and audit trails. Returns ErrWorkflowNotFound if there is no queryable
	// current execution for workflowID — see ErrWorkflowNotFound's doc for what that covers.
	GetStatus(ctx context.Context, workflowID string) (*WorkflowInstance, error)
}

// AdminInterventionResolver is implemented by managers that support resolving a node parked
// in NodeStatusAwaitingAdmin. It is kept separate from Manager so that existing Manager
// implementations (e.g. test doubles) aren't forced to implement it. Callers that want this
// capability can type-assert: if r, ok := mgr.(AdminInterventionResolver); ok { ... }.
type AdminInterventionResolver interface {
	// ResolveAdminIntervention sends an admin's resolution decision to a node that is
	// currently parked in NodeStatusAwaitingAdmin, identified by workflowID/runID/nodeID.
	ResolveAdminIntervention(ctx context.Context, workflowID, runID string, resolution AdminResolutionSignal) error
}

// TemporalManager extends the Manager interface with worker control methods.
type TemporalManager interface {
	Manager

	// RegisterDefinitionHandler registers the handler function for fetching sub-workflow definitions.
	RegisterDefinitionHandler(handler func(templateID string) (WorkflowDefinition, error))

	// RegisterAdminParkHandler registers the handler invoked whenever a node parks for admin
	// intervention. Optional — see AdminParkHandler's doc for what a nil/unregistered handler
	// means.
	RegisterAdminParkHandler(handler AdminParkHandler)

	// StartWorker connects the internal Temporal Worker to the Temporal Server and
	// begins polling the task queue for workflow and activity tasks.
	StartWorker() error

	// StopWorker gracefully shuts down the internal Temporal Worker, stopping it from
	// pulling new tasks while allowing currently executing tasks to finish.
	StopWorker()
}

type temporalManagerImpl struct {
	temporalClient client.Client
	worker         worker.Worker
	namespace      string
	taskQueue      string
	activities     *Activities
}

// NewTemporalManager creates a new instance of TemporalManager.
//
// namespace must be the namespace c was dialed with. client.Client does not expose it, and
// some calls (e.g. CompleteActivityByID) require it explicitly.
func NewTemporalManager(
	c client.Client,
	namespace string,
	taskQueue string,
	taskHandler TaskActivationHandler,
	completionHandler WorkflowCompletionHandler) TemporalManager {
	if strings.TrimSpace(namespace) == "" {
		panic("namespace must not be empty")
	}
	if strings.TrimSpace(taskQueue) == "" {
		panic("taskQueue must not be empty")
	}

	m := &temporalManagerImpl{
		temporalClient: c,
		namespace:      namespace,
		taskQueue:      taskQueue,
	}

	w := worker.New(c, taskQueue, worker.Options{})

	w.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})

	acts := &Activities{
		ExecuteTaskActivityHandler:       taskHandler,
		WorkflowCompletedActivityHandler: completionHandler,
	}
	w.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	w.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	w.RegisterActivityWithOptions(acts.FetchWorkflowDefinitionActivity, activity.RegisterOptions{Name: "FetchWorkflowDefinitionActivity"})
	w.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})

	m.worker = w
	m.activities = acts
	slog.Info("temporal manager initialized", "namespace", namespace, "task_queue", taskQueue)
	return m
}

func (m *temporalManagerImpl) RegisterDefinitionHandler(handler func(templateID string) (WorkflowDefinition, error)) {
	m.activities.FetchWorkflowDefinitionHandler = handler
}

func (m *temporalManagerImpl) RegisterAdminParkHandler(handler AdminParkHandler) {
	m.activities.AdminParkHandler = handler
}

func (m *temporalManagerImpl) StartWorkflow(ctx context.Context, ID string, def WorkflowDefinition, initialWorkflowVariables map[string]any) error {
	opts := client.StartWorkflowOptions{
		ID:        ID,
		TaskQueue: m.taskQueue,
	}

	_, err := m.temporalClient.ExecuteWorkflow(ctx, opts, "GraphInterpreterWorkflow", def, initialWorkflowVariables)
	if err != nil {
		return fmt.Errorf("failed to execute workflow: %w", err)
	}

	return nil
}

// TaskDone is invoked by the external application to complete a dormant asynchronous Temporal Activity.
// WorkflowID is the ID of the workflow
// runID is the ID of the run
// nodeID is the ID of the node
// output is the key valye pairs that should be added to the global context
func (m *temporalManagerImpl) TaskDone(ctx context.Context, workflowID, runID, nodeID string, output map[string]any) error {
	return m.temporalClient.CompleteActivityByID(ctx, m.namespace, workflowID, runID, nodeID, output, nil)
}

func (m *temporalManagerImpl) TaskUpdate(ctx context.Context, workflowID, runID string, event UpdateEvent) error {
	return m.temporalClient.SignalWorkflow(ctx, workflowID, runID, "TaskUpdateSignal", event)
}

func (m *temporalManagerImpl) ResolveAdminIntervention(ctx context.Context, workflowID, runID string, resolution AdminResolutionSignal) error {
	return m.temporalClient.SignalWorkflow(ctx, workflowID, runID, AdminResolutionSignalName, resolution)
}

func (m *temporalManagerImpl) GetStatus(ctx context.Context, workflowID string) (*WorkflowInstance, error) {
	val, err := m.temporalClient.QueryWorkflow(ctx, workflowID, "", "GetStatus")
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return nil, fmt.Errorf("%w: %w", ErrWorkflowNotFound, err)
		}
		return nil, err
	}

	var instance WorkflowInstance
	if err := val.Get(&instance); err != nil {
		return nil, err
	}

	return &instance, nil
}

func (m *temporalManagerImpl) StartWorker() error {
	if err := m.worker.Start(); err != nil {
		return err
	}
	slog.Info("temporal worker started", "task_queue", m.taskQueue)
	return nil
}

func (m *temporalManagerImpl) StopWorker() {
	m.worker.Stop()
	slog.Info("temporal worker stopped", "task_queue", m.taskQueue)
}
