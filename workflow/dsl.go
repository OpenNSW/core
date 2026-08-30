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
	NodeTypeTimer     NodeType = "TIMER"
	NodeTypeSignaling NodeType = "SIGNALING"
)

// SignalingType represents the sub-type of a SIGNALING node.
type SignalingType string

// Signaling sub-types.
const (
	SignalingTypeEmit SignalingType = "EMIT" // Emits a signal to sibling branches
	SignalingTypeWait SignalingType = "WAIT" // Blocks until a named signal is received
)

// SplitMode represents the split task dynamic fan-out mode.
type SplitMode string

// Core split task execution modes.
const (
	// SplitModeSameTemplate spawns one child workflow per item running the same external template.
	//
	// Deprecated: Prefer using BATCH_SPLIT gateway nodes instead. BATCH_SPLIT defines sub-graphs
	// inline, supports conditional partitioning, aggregates tasks over item slices, and merges results
	// by item ID without requiring external template registration.
	SplitModeSameTemplate SplitMode = "SAME_TEMPLATE"

	// SplitModeDifferentTemplates spawns child workflows dynamically based on each item's template_id.
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

// TimerConfig defines how long a TIMER node waits before following its single
// outgoing edge. The wait is a durable Temporal timer, so it survives worker
// restarts and holds no activity or worker slot while it runs.
type TimerConfig struct {
	// Duration is how long to wait, as a Go duration string ("30s", "1m", "2h").
	// Required, and must be positive.
	Duration string `json:"duration"`

	// CounterKey is the workflow-variable dot-path the node writes its fire
	// count to, so a downstream gateway can bound a polling loop (for example
	// "status.poll_attempts" against a condition of "< 60"). The count is
	// written before the wait begins and starts at 1. Defaults to
	// "<node id>.iterations".
	CounterKey string `json:"counter_key,omitempty"`
}

// SignalingConfig defines the parameters for a SIGNALING node. SIGNALING nodes
// let sibling branches spawned by the same SPLIT_TASK node coordinate with each
// other: one hop up to the parent, one hop back down to that node's other children.
// They do not bubble further up an ancestor chain or cascade down into a sibling's
// own nested sub-splits — see the "Signaling Nodes" section in README.md.
type SignalingConfig struct {
	// Type is either EMIT or WAIT.
	Type SignalingType `json:"type"`
	// SignalName is the name of the signal channel. Required.
	SignalName string `json:"signal_name"`
	// Payload holds the data to emit (EMIT nodes only). Ignored for WAIT nodes.
	Payload map[string]any `json:"payload,omitempty"`
}

// GatewayType represents the type of a gateway controlling execution flow.
type GatewayType string

// Gateway types controlling branching and merging.
const (
	GatewayTypeExclusiveSplit GatewayType = "EXCLUSIVE_SPLIT" // XOR Split
	GatewayTypeParallelSplit  GatewayType = "PARALLEL_SPLIT"  // AND Split
	GatewayTypeExclusiveJoin  GatewayType = "EXCLUSIVE_JOIN"  // XOR Join
	GatewayTypeParallelJoin   GatewayType = "PARALLEL_JOIN"   // AND Join
	GatewayTypeBatchSplit     GatewayType = "BATCH_SPLIT"     // Item-partitioning Split
	GatewayTypeBatchJoin      GatewayType = "BATCH_JOIN"      // Item-merging Join
)

// Node represents a step in the workflow graph.
type Node struct {
	ID             string            `json:"id"`
	Type           NodeType          `json:"type"`                       // See NodeType constants
	GatewayType    GatewayType       `json:"gateway_type,omitempty"`     // See Gateway Types constants
	TaskTemplateID string            `json:"task_template_id,omitempty"` // Identifier for the task template to run
	InputMapping   map[string]string `json:"input_mapping,omitempty"`    // Maps WorkflowVariables Key -> Task Input Key
	OutputMapping  map[string]string `json:"output_mapping,omitempty"`   // Maps Task Output Key -> WorkflowVariables Key

	// Extensions
	SplitTask    *SplitTaskConfig    `json:"split_task,omitempty"`
	Timer        *TimerConfig        `json:"timer,omitempty"`
	Signaling    *SignalingConfig    `json:"signaling,omitempty"`
	BatchGateway *BatchGatewayConfig `json:"batch_gateway,omitempty"`
	BatchJoin    *BatchJoinConfig    `json:"batch_join,omitempty"`
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

// BatchGatewayConfig configures item-level partitioning for a BATCH_SPLIT gateway node.
type BatchGatewayConfig struct {
	// ItemsVariable is the dot-path in WorkflowVariables pointing to the item slice.
	// Defaults to "_items" if empty.
	ItemsVariable string `json:"items_variable,omitempty"`

	// IDField is the field name within each item used as the unique identifier for
	// ID-based merging at the paired BATCH_JOIN. Defaults to "id" if empty.
	IDField string `json:"id_field,omitempty"`
}

// BatchJoinConfig configures how a BATCH_JOIN gateway merges child partition results.
type BatchJoinConfig struct {
	// GatewayNodeID is the node ID of the paired BATCH_SPLIT gateway.
	GatewayNodeID string `json:"gateway_node_id"`

	// ItemsVariable is the dot-path in WorkflowVariables pointing to the item slice.
	// Must match the paired BATCH_SPLIT's items_variable. Defaults to "_items" if empty.
	ItemsVariable string `json:"items_variable,omitempty"`

	// IDField is the field name within each item used as the unique identifier.
	// Must match the paired BATCH_SPLIT's id_field. Defaults to "id" if empty.
	IDField string `json:"id_field,omitempty"`
}

// VarScopePath is the workflow variable key holding the hierarchical scope path string
// for batch gateway nesting (e.g. "root/gw_type/lab/gw_result/fail").
const VarScopePath = "_scope_path"

// Default values for BatchGatewayConfig / BatchJoinConfig fields.
const (
	DefaultItemsVariable = "_items"
	DefaultIDField       = "id"
)

// Engine-level guardrail defaults for batch gateways.
const (
	// DefaultMaxBatchDepth is the maximum nesting depth of BATCH_SPLIT gateways.
	DefaultMaxBatchDepth = 4
	// DefaultMaxChildrenPerGateway is the maximum number of partitions a single
	// BATCH_SPLIT gateway may spawn.
	DefaultMaxChildrenPerGateway = 20
)
