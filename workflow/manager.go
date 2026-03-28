package engine

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// ExecutionStatus defines the allowed states for a workflow instance.
type ExecutionStatus string

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
	// NodeID is the ID of the graph node currently being executed.
	NodeID string
	// TaskTemplateID identifies the specific type of external work/script the task executor should run.
	TaskTemplateID string
	// Inputs contains the specific subset of WorkflowVariables mapped to this task's requirements.
	Inputs map[string]any
}

type NodeStatus string

const (
	NodeStatusNotStarted NodeStatus = "NOT_STARTED"
	NodeStatusRunning    NodeStatus = "RUNNING"
	NodeStatusCompleted  NodeStatus = "COMPLETED"
	NodeStatusFailed     NodeStatus = "FAILED"
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
// The implementation should trigger the external system (e.g., via HTTP, Kafka, or DB insert)
// and must return quickly. The workflow node will then pause asynchronously
// until the host application calls Manager.TaskDone() with the matching IDs.
type TaskActivationHandler func(payload TaskPayload) error

// WorkflowCompletionHandler is invoked when the generic DAG workflow successfully reaches an "End" node,
// providing the final, accumulated state of the workflow variables.
type WorkflowCompletionHandler func(workflowID string, finalWorkflowVariables map[string]any) error

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
	// current variables, and audit trails.
	GetStatus(ctx context.Context, workflowID string) (*WorkflowInstance, error)
}

type TemporalManager interface {
	Manager

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
}

func NewTemporalManager(
	c client.Client,
	taskQueue string,
	taskHandler TaskActivationHandler,
	completionHandler WorkflowCompletionHandler) TemporalManager {
	m := &temporalManagerImpl{
		temporalClient: c,
	}

	w := worker.New(c, taskQueue, worker.Options{})

	w.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})

	acts := &EngineActivities{ExecuteTaskActivityHandler: taskHandler, WorkflowCompletedActivityHandler: completionHandler}
	w.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	w.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	m.worker = w
	return m
}

func (m *temporalManagerImpl) StartWorkflow(ctx context.Context, ID string, def WorkflowDefinition, initialWorkflowVariables map[string]any) error {
	opts := client.StartWorkflowOptions{
		ID:        ID,
		TaskQueue: "INTERPRETER_TASK_QUEUE",
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
	return m.temporalClient.CompleteActivityByID(ctx, "default", workflowID, runID, nodeID, output, nil)
}

func (m *temporalManagerImpl) TaskUpdate(ctx context.Context, workflowID, runID string, event UpdateEvent) error {
	return m.temporalClient.SignalWorkflow(ctx, workflowID, runID, "TaskUpdateSignal", event)
}

func (m *temporalManagerImpl) GetStatus(ctx context.Context, workflowID string) (*WorkflowInstance, error) {
	val, err := m.temporalClient.QueryWorkflow(ctx, workflowID, "", "GetStatus")
	if err != nil {
		return nil, err
	}

	var instance WorkflowInstance
	if err := val.Get(&instance); err != nil {
		return nil, err
	}

	return &instance, nil
}

func (m *temporalManagerImpl) StartWorker() error {
	return m.worker.Start()
}

func (m *temporalManagerImpl) StopWorker() {
	m.worker.Stop()
}
