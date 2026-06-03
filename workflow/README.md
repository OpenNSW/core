# Go Temporal Workflow Graph Interpreter Engine

A powerful, JSON-DSL-driven graph interpreter engine built on top of the Go [Temporal SDK](https://go.temporal.io/sdk). This engine dynamically executes complex directed-acyclic-graph (DAG) workflows defined in a JSON specification without requiring code redeployment.

## Key Features

- **DSL-Driven DAG Execution**: Runs workflows represented by structured nodes and conditional edges.
- **Multiple Node Types**:
  - **`START` / `END`**: Standard execution entry and exit points.
  - **`TASK`**: Executes application activities. Supports synchronous/asynchronous work execution.
  - **`GATEWAY`**: Controls logical branching and joining (`EXCLUSIVE_SPLIT`, `PARALLEL_SPLIT`, `EXCLUSIVE_JOIN`, `PARALLEL_JOIN`).
  - **`SPLIT_TASK`**: Spawns multiple parallel child workflows dynamically (dynamic fan-out). Supports:
    - `SAME_TEMPLATE`: Homogeneous splits running the same template across payloads.
    - `DIFFERENT_TEMPLATES`: Poly-workflow / heterogeneous splits running different templates dynamically.
    - Failure handling configurations (`FAIL_FAST` or `COLLECT_ALL`).
- **Flexible Data Mapping**: Automatically maps keys between the global `WorkflowVariables` dictionary and task input/output scopes using `InputMapping` and `OutputMapping`.
- **Query & Signal Support**: Provides real-time queries for workflow execution state snapshots (`WorkflowInstance`) and processes external update events asynchronously.

---

## Architecture Overview

```mermaid
graph TD
    Start([Start Node]) --> Task1[Task Node]
    Task1 --> Gate1{Gateway: Split}
    Gate1 -->|Condition A| TaskA[Task A]
    Gate1 -->|Condition B| TaskB[Task B]
    TaskA --> Gate2{Gateway: Join}
    TaskB --> Gate2
    Gate2 --> Split1[Split Task Node]
    Split1 -->|Child Workflow 1| Child1[Child Instance]
    Split1 -->|Child Workflow 2| Child2[Child Instance]
    Child1 --> End([End Node])
    Child2 --> End
```

---

## DSL Specification (`dsl.go`)

Workflows are defined through the [WorkflowDefinition](https://github.com/OpenNSW/go-temporal-workflow/blob/main/dsl.go) structure.

### Node Config Definition
```go
type Node struct {
	ID             string            `json:"id"`
	Type           NodeType          `json:"type"`                       // START, END, TASK, GATEWAY, or SPLIT_TASK
	GatewayType    GatewayType       `json:"gateway_type,omitempty"`     // EXCLUSIVE_SPLIT, PARALLEL_SPLIT, etc.
	TaskTemplateID string            `json:"task_template_id,omitempty"` // ID of the task template to run
	InputMapping   map[string]string `json:"input_mapping,omitempty"`    // Maps WorkflowVariables -> Task Input Key
	OutputMapping  map[string]string `json:"output_mapping,omitempty"`   // Maps Task Output -> WorkflowVariables Key
	SplitTask      *SplitTaskConfig  `json:"split_task,omitempty"`       // Configuration for dynamic fan-out splits
}
```

### Dynamic Fan-out Configuration (`SplitTaskConfig`)
```go
type SplitTaskConfig struct {
	Mode            SplitMode   `json:"mode"`                       // SAME_TEMPLATE or DIFFERENT_TEMPLATES
	ItemsVariable   string      `json:"items_variable"`             // Global variables dot-path pointing to []map[string]any
	ResultsVariable string      `json:"results_variable,omitempty"` // Destination variable to save aggregated sub-workflow outputs
	FailureMode     FailureMode `json:"failure_mode"`               // FAIL_FAST or COLLECT_ALL
	IterationKey    string      `json:"iteration_key,omitempty"`    // Sub-context namespace key (defaults to "_iter")
}
```

---

## Integration Setup

To run the engine inside a Go application, initialize the `TemporalManager` with your task and completion handlers:

```go
import "github.com/OpenNSW/go-temporal-workflow"

// Initialize the TemporalManager (this automatically registers the workflow and activities internally)
manager := engine.NewTemporalManager(
    temporalClient,
    "your-task-queue",
    taskHandler,       // TaskActivationHandler
    completionHandler, // WorkflowCompletionHandler
)

// Register sub-workflow definition loader (required if using SPLIT_TASK nodes)
manager.RegisterDefinitionHandler(func(templateID string) (engine.WorkflowDefinition, error) {
    // Retrieve definition from database or local files
    return loadDefinition(templateID), nil
})

// Start the internal worker to begin execution
err := manager.StartWorker()
if err != nil {
    log.Fatalf("Failed to start worker: %v", err)
}
```

## Running Tests

Run the integration suite locally to verify engine features:
```bash
go test -race -v ./...
```
