# taskflow

A modular task orchestration engine that sits between a macro business workflow (the `workflow` package) and the individual interactive steps it activates. It manages the full lifecycle of human-in-the-loop tasks: form submission, payment, external review, and custom plugin-driven steps.

## Conceptual model

```
[Parent Workflow]     ← macro business journey (workflow package)
        │
        ▼  StartTask
[TaskManager] ──────► [TaskRecord in DB]
        │
        ▼  StartTaskWorkflow
[Task Workflow]       ← micro interactive journey (Temporal workflow)
        │
        ▼  StartSubTask
[SubTask Node]        ← single interaction step (form, payment, external review, …)
        │
        ▼  CompleteTaskStep (called by portal)
[Resume & Continue]
        │
        ▼  Task workflow ends
[HandleTaskCompletion]
        │
        ▼  onTaskCompleted callback
[Resume Parent Workflow]
```

- **Task** — a self-contained micro-flow (its own Temporal workflow) that fulfils one TASK node in the parent workflow.
- **SubTask** — an individual node inside a task workflow, executed by a registered **plugin** (`USER_INPUT`, `PAYMENT`, `EXTERNAL_REVIEW`, or your own).
- **No parallel subtasks.** A `TaskRecord` has exactly one active subtask at a time.

## Sub-packages

| Package | Purpose |
|---|---|
| `taskflow/orchestrator` | `TaskManager` — the main API surface |
| `taskflow/plugins` | Plugin interface, registry, and built-in plugins |
| `taskflow/store` | `TaskStore` interface and `TaskRecord` |
| `taskflow/store/gorm` | GORM/PostgreSQL implementation of `TaskStore` |
| `taskflow/renderer` | `Renderer` interface consumed by the orchestrator |
| `taskflow/renderer/zoneview` | Zone-based renderer backed by `uiprojector` |
| `taskflow/types` | `TaskTemplate` and `SubTaskTemplate` config types |

## Wiring

```go
import (
    "github.com/OpenNSW/core/taskflow/orchestrator"
    "github.com/OpenNSW/core/taskflow/plugins"
    gormstore "github.com/OpenNSW/core/taskflow/store/gorm"
    "github.com/OpenNSW/core/taskflow/renderer/zoneview"
)

// 1. Task store
store := gormstore.New(db)

// 2. Plugin registry
pluginRegistry := plugins.NewRegistry()
pluginRegistry.Register("USER_INPUT",      plugins.NewUserInputPlugin())
pluginRegistry.Register("API_CALL",        plugins.NewAPICallPlugin(remoteManager))
pluginRegistry.Register("PAYMENT",         NewPaymentPlugin(paymentService))
pluginRegistry.Register("EXTERNAL_REVIEW", NewExternalReviewPlugin(remoteManager))

// 3. Renderer
assembler, _ := uiprojector.NewAssembler(templateProvider, uiprojector.DefaultProjectors())
taskRenderer := zoneview.NewTaskRenderer(assembler)

// 4. Wire the micro-workflow runner and task manager together.
// tm is forward-declared so the handler closures can reference it before it's assigned.
var tm *orchestrator.TaskManager
workflowRunner := workflow.NewTemporalManager(
    temporalClient,
    "MICRO_WORKFLOW_QUEUE",
    func(payload workflow.TaskPayload) (map[string]any, error) {
        return tm.StartSubTask(context.Background(), payload)
    },
    func(workflowID string, vars map[string]any) error {
        return tm.HandleTaskCompletion(context.Background(), workflowID, vars)
    },
)

onTaskCompleted := func(parentWorkflowID, parentRunID, parentNodeID string, vars map[string]any) error {
    return parentWorkflowManager.TaskDone(ctx, parentWorkflowID, parentRunID, parentNodeID, vars)
}
tm = orchestrator.NewTaskManager(store, artifactRegistry, pluginRegistry, workflowRunner, onTaskCompleted, taskRenderer)

// 5. Start the Temporal worker
if err := workflowRunner.StartWorker(); err != nil {
    log.Fatal(err)
}
```

## TaskManager API

| Method | Called by | Purpose |
|---|---|---|
| `StartTask(payload)` | Parent workflow activity | Create task record, start micro-workflow |
| `StartSubTask(payload)` | Micro-workflow node | Activate subtask, route to plugin |
| `HandleTaskCompletion(ctx, workflowID, vars)` | Micro-workflow on exit | Mark complete, fire callback to parent |
| `GetTaskRenderInfo(ctx, taskID)` | Portal HTTP handler | Fetch task + rendered UI |
| `CompleteTaskStep(ctx, taskID, payload)` | Portal HTTP handler | Submit form/interaction, resume subtask |
| `GetAllTasks(ctx, parentWorkflowID)` | Portal HTTP handler | List tasks for a workflow instance |

## Writing a plugin

```go
import "github.com/OpenNSW/core/taskflow/plugins"

type MyPlugin struct{ remoteManager *remote.Manager }

func (p *MyPlugin) Execute(ctx plugins.PluginContext, config json.RawMessage) error {
    var cfg struct{ ServiceID string `json:"service_id"` }
    json.Unmarshal(config, &cfg)

    var resp MyResponse
    if err := p.remoteManager.Call(ctx.Context, cfg.ServiceID, req, &resp); err != nil {
        return err
    }

    // Return ErrSuspended to park the task and wait for an external callback.
    // Return nil to proceed to the next subtask immediately (synchronous plugin).
    return plugins.ErrSuspended
}
```

Register it: `pluginRegistry.Register("MY_PLUGIN", &MyPlugin{remoteManager: rm})`

## Key invariants

- **`TaskID` equals the parent workflow's `NodeID`.** The task is addressable by an ID the caller already holds.
- **Plugins suspend with `plugins.ErrSuspended`.** Synchronous plugins return `nil`; the workflow advances without waiting.
- **`StartTask` returns `activity.ErrResultPending`** on the happy path. The parent activity suspends until `onTaskCompleted` fires.
- **Submission payloads are scoped** to the active subtask's `OutputNamespace` in `TaskRecord.Data`. Callers send a raw object; the server stamps the correct key.

## Task-level workflow constraint: no parallel paths

**Task workflows must be strictly sequential.** A `TaskRecord` stores coordinates for exactly one active subtask at a time (`TaskWorkflowID`, `TaskRunID`, `SubTaskNodeID`). Using a `PARALLEL_SPLIT` gateway inside a task workflow would activate multiple subtask nodes simultaneously, but the record can only point at one — the others would be unreachable via `CompleteTaskStep` and the workflow would hang.

`StartTask` enforces this at launch time: if the workflow definition contains a `PARALLEL_SPLIT` gateway node it returns an error immediately, before creating any DB record.

**Allowed gateway types inside task workflows:**

| Gateway | Allowed | Notes |
|---|---|---|
| `EXCLUSIVE_SPLIT` | ✓ | Conditional branching is fine |
| `EXCLUSIVE_JOIN` | ✓ | Merging sequential branches is fine |
| `PARALLEL_SPLIT` | ✗ | Rejected at `StartTask` — would create unreachable subtasks |
| `PARALLEL_JOIN` | ✗ | No parallel split means a parallel join can never be reached either |

If you need parallel work inside a task, model each parallel branch as a separate TASK node in the **parent** (macro) workflow — the parent workflow engine supports `PARALLEL_SPLIT` via child workflow fan-out.
