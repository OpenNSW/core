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
	DefaultIterationKey = "_iter"

	// Keys injected into the child's workspace variables
	VarSplitNodeID      = "_split_node_id"
	VarParentWorkflowID = "_parent_workflow_id"

	// Iteration context sub-keys
	IterIndexKey    = "index"
	IterBranchIDKey = "branch_id"
	IterInputKey    = "input"
)

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
