// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

// NodeType represents the type of a workflow node (e.g. START, END, TASK, GATEWAY).
type NodeType string

// Core node types supported by the engine.
const (
	NodeTypeStart     NodeType = "START"
	NodeTypeEnd       NodeType = "END"
	NodeTypeTask      NodeType = "TASK"
	NodeTypeGateway   NodeType = "GATEWAY"
	NodeTypeSplitTask NodeType = "SPLIT_TASK"
)

// SplitMode represents the split task dynamic fan-out mode.
type SplitMode string

// Core split task execution modes.
const (
	SplitModeSameTemplate       SplitMode = "SAME_TEMPLATE"
	SplitModeDifferentTemplates SplitMode = "DIFFERENT_TEMPLATES"
)

// FailureMode represents the failure handling strategy for split task executions.
type FailureMode string

// Core split task failure handling modes.
const (
	FailureModeFailFast   FailureMode = "FAIL_FAST"
	FailureModeCollectAll FailureMode = "COLLECT_ALL"
)

// SplitTaskItem defines the structure for individual branch items inside the items collection.
type SplitTaskItem struct {
	TemplateID string         `json:"template_id"`
	BranchID   string         `json:"branch_id"`
	Payload    map[string]any `json:"payload"`
}

// Core structural execution constants
const (
	// DefaultIterationKey is the default variable name injected into a child workflow's
	// state containing iteration details (e.g., _iter.index, _iter.branch_id, _iter.input).
	DefaultIterationKey = "_iter"
	// ChildBroadcastSignalName is the base name of the Temporal signal used to route cross-branch
	// signals from a child workflow back up to the parent for brokerage to other sibling branches.
	// It is scoped per SPLIT_TASK node via childBroadcastSignalName so that two SPLIT_TASK nodes
	// running concurrently in the same workflow execution (e.g. under a PARALLEL_SPLIT gateway)
	// don't share a channel and cross-deliver each other's broadcasts.
	ChildBroadcastSignalName = "child_broadcast_signal"

	// Keys injected into the child's workspace variables
	// VarSplitNodeID identifies the ID of the SplitTask node in the parent workflow.
	VarSplitNodeID = "_split_node_id"
	// VarParentWorkflowID contains the workflow ID of the parent/orchestrator workflow.
	VarParentWorkflowID = "_parent_workflow_id"
	// VarBranchID contains the unique branch ID assigned to the specific child workflow branch.
	VarBranchID = "_branch_id"

	// Iteration context sub-keys (e.g., used to access _iter.index, _iter.branch_id, _iter.input)
	// IterIndexKey is the sub-key for the 0-based index of this branch within the items array.
	IterIndexKey = "index"
	// IterBranchIDKey is the sub-key for the unique branch identifier.
	IterBranchIDKey = "branch_id"
	// IterInputKey is the sub-key pointing to the input payload mapped to this branch.
	IterInputKey = "input"

	// System task template IDs. SysTaskEmitSignal and SysTaskWaitForSignal let sibling
	// branches spawned by the same SPLIT_TASK node coordinate: one hop up to the parent,
	// one hop back down to that node's other children. They do not bubble further up an
	// ancestor chain or cascade down into a sibling's own nested sub-splits — see the
	// "System Task Templates" section in README.md for the full scope.
	//
	// SysTaskWaitForSignal is the template ID for the built-in system task that suspends
	// the workflow until a specific signal is received from a sibling branch.
	SysTaskWaitForSignal = "sys:wait_for_signal"
	// SysTaskEmitSignal is the template ID for the built-in system task that publishes/emits
	// a signal to be routed to sibling branches under the same SPLIT_TASK node.
	SysTaskEmitSignal = "sys:emit_signal"

	// Input keys for system tasks
	// InputSignalName is the parameter key used to specify the target signal name in signal tasks.
	InputSignalName = "signal_name"
	// InputPayload is the parameter key used to specify the data payload in emit signal tasks.
	InputPayload = "payload"
)

// BroadcastMessage defines a unified Message Wrapper for parent brokerage.
type BroadcastMessage struct {
	SenderBranchID string         `json:"sender_branch_id"`
	SignalName     string         `json:"signal_name"`
	Payload        map[string]any `json:"payload"`
}

// SplitTaskConfig defines dynamic fan-out execution configuration.
type SplitTaskConfig struct {
	Mode            SplitMode   `json:"mode"`                       // SAME_TEMPLATE or DIFFERENT_TEMPLATES
	ItemsVariable   string      `json:"items_variable"`             // Global context variable dot-path pointing to []map[string]any
	ResultsVariable string      `json:"results_variable,omitempty"` // Destination path to save aggregated sub-workflow outputs
	FailureMode     FailureMode `json:"failure_mode"`               // FAIL_FAST or COLLECT_ALL
	IterationKey    string      `json:"iteration_key,omitempty"`    // Override key for sub-context namespace. Defaults to "_iter"
}

// GatewayType represents the type of a gateway controlling execution flow.
type GatewayType string

// Gateway types controlling branching and merging.
const (
	GatewayTypeExclusiveSplit GatewayType = "EXCLUSIVE_SPLIT" // XOR Split
	GatewayTypeParallelSplit  GatewayType = "PARALLEL_SPLIT"  // AND Split
	GatewayTypeExclusiveJoin  GatewayType = "EXCLUSIVE_JOIN"  // XOR Join
	GatewayTypeParallelJoin   GatewayType = "PARALLEL_JOIN"   // AND Join
)

// Node represents a step in the workflow graph.
type Node struct {
	ID             string            `json:"id"`
	Type           NodeType          `json:"type"`                       // START, END, TASK, GATEWAY, or SPLIT_TASK
	GatewayType    GatewayType       `json:"gateway_type,omitempty"`     // See Gateway Types constants
	TaskTemplateID string            `json:"task_template_id,omitempty"` // Identifier for the task template to run
	InputMapping   map[string]string `json:"input_mapping,omitempty"`    // Maps WorkflowVariables Key -> Task Input Key
	OutputMapping  map[string]string `json:"output_mapping,omitempty"`   // Maps Task Output Key -> WorkflowVariables Key

	// Extensions
	SplitTask *SplitTaskConfig `json:"split_task,omitempty"`
}

// Edge represents a directed connection between two nodes.
type Edge struct {
	ID        string `json:"id"`
	SourceID  string `json:"source_id"`
	TargetID  string `json:"target_id"`
	Condition string `json:"condition,omitempty"` // Expression mapped against WorkflowVariables
}

// WorkflowDefinition represents the structural blueprint of a workflow process.
// It serves as the parsed representation of the JSON DSL, defining how nodes
// and edges form a directed graph for the execution engine.
type WorkflowDefinition struct {
	// ID is the unique identifier for this specific workflow template.
	ID string `json:"id"`

	// Name is a human-readable label used for display and organizational purposes.
	Name string `json:"name"`

	// Version tracks iterations of the workflow logic, allowing for side-by-side
	// deployment of different logic versions.
	Version int `json:"version"`

	// Nodes defines the individual steps, gateways, and boundary events
	// that make up the workflow.
	Nodes []Node `json:"nodes"`

	// Edges defines the directed connections between nodes, including
	// any conditional logic required for branching.
	Edges []Edge `json:"edges"`
}
