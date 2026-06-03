# Integration Guide

How to embed the orchestrator in your own Go service.

> Prerequisite: read [`architecture.md`](architecture.md) first. This guide assumes you understand the Parent / Task / SubTask layering and the `TaskRecord` shape.

---

## What you're building

To run the orchestrator, you provide concrete implementations of four dependencies and one callback:

| Dependency        | Interface                            | What it does                                                   |
|-------------------|--------------------------------------|----------------------------------------------------------------|
| Task store        | `store.TaskStore`                    | Persists `TaskRecord`s                                         |
| Template registry | `orchestrator.TaskTemplateRegistry`  | Looks up task / subtask / workflow / render-config definitions |
| Plugin registry   | `*plugins.Registry` (concrete)       | Maps `TaskType` → plugin handler                               |
| Temporal manager  | `engine.TemporalManager`             | Starts task workflows and resumes parked activities            |
| Renderer          | `renderer.Renderer`                  | Turns `(state, data)` + render config → a UI view              |
| Callback          | `orchestrator.TaskCompletedCallback` | Wakes the parent workflow when a task finishes                 |

Then you call:

```go
tm := orchestrator.NewTaskManager(db, registry, pluginsReg, taskWorkflowManager, onTaskCompleted, rdr)
```

…and wire `tm.StartTask`, `tm.StartSubTask`, and `tm.HandleTaskCompletion` into your Temporal handlers. `demo/main.go` is the complete worked example.

---

## 1. Implementing `TaskStore`

```go
type TaskStore interface {
    SaveTask(ctx context.Context, record TaskRecord)
    GetTask(ctx context.Context, taskID string) (TaskRecord, bool)
    GetTaskByWorkflowID(ctx context.Context, workflowID string) (TaskRecord, bool)
    GetAllTasks(ctx context.Context, parentWorkflowID string) []TaskRecord
}
```

**Contracts:**

- **`SaveTask` is an upsert** keyed on `record.TaskID`. Called for both new and updated records. It is the only write path — if you implement durable storage, this is your `INSERT … ON CONFLICT DO UPDATE`. The signature returns no error today; if your store can fail, log and surface failures via your own observability — the orchestrator currently treats persistence as best-effort.
- **`GetTask`** looks up by `TaskID` (which equals the parent's NodeID — see architecture doc). Must return `(zero, false)` when absent, never panic.
- **`GetTaskByWorkflowID`** looks up by `TaskWorkflowID` (the child workflow's Temporal ID). Used internally by `StartSubTask` and `HandleTaskCompletion`. It must scan or index by that field, not by `TaskID`.
- **`GetAllTasks`** returns every record if `parentWorkflowID == ""`, otherwise only records where `record.ParentWorkflowID == parentWorkflowID`. Used by the portal listing API.

A minimal in-memory implementation is in `demo/db.go` — useful as a starting point or for tests.

### Concurrency

`SaveTask` and the various reads are called from multiple goroutines (Temporal worker pool, HTTP handlers). Your implementation must be safe for concurrent use. The demo uses a `sync.RWMutex`; a SQL-backed store gets this from the DB.

### What "updated" means

The orchestrator never modifies a record outside `SaveTask`. Plugins mutate the `TaskRecord` pointer they receive in `PluginContext.Record`, and the orchestrator calls `SaveTask` after the plugin returns. You don't need to diff or track changes — just persist what you're handed.

---

## 2. Implementing `TaskTemplateRegistry`

```go
type TaskTemplateRegistry interface {
    GetTaskTemplate(id string) (TaskTemplate, bool)
    GetSubTaskTemplate(id string) (SubTaskTemplate, bool)
    GetWorkflow(id string) (engine.WorkflowDefinition, bool)
    GetGenericTemplate(id string) (json.RawMessage, bool)
}
```

This is read-only from the orchestrator's perspective. How definitions get *into* your registry is your call — load from disk at startup (the demo does this with `demo/templates/*.json`), pull from a config service, hard-code them, etc.

| Method               | Resolves                                          | Used in                                                  |
|----------------------|---------------------------------------------------|----------------------------------------------------------|
| `GetTaskTemplate`    | `payload.TaskTemplateID` from the parent workflow | `StartTask`                                              |
| `GetSubTaskTemplate` | `payload.TaskTemplateID` from the child workflow  | `StartSubTask`                                           |
| `GetWorkflow`        | `TaskTemplate.WorkflowID`                         | `StartTask`                                              |
| `GetGenericTemplate` | `TaskTemplate.RenderConfigID`                     | `StartTask` (snapshotted into `TaskRecord.RenderConfig`) |

The render config is **snapshotted** into the `TaskRecord` at start time. If you mutate a render config in the registry, existing tasks keep their original view — which is the desired behaviour for audit and replay.

See [`template-reference.md`](template-reference.md) for the JSON shapes.

---

## 3. Registering plugins

```go
pluginsReg := plugins.NewRegistry()
pluginsReg.Register("USER_INPUT", plugins.NewUserInputPlugin())
pluginsReg.Register("EXTERNAL_REVIEW", plugins.NewExternalReviewPlugin(dispatcher))
pluginsReg.Register("PAYMENT", plugins.NewPaymentPlugin(dispatcher))
pluginsReg.Register("FIRE_AND_FORGET", plugins.NewAPICallPlugin(dispatcher))
```

The key — `"USER_INPUT"`, `"PAYMENT"`, etc. — is the `task_type` field in the **SubTaskTemplate** JSON. When the task workflow activates a subtask node, the orchestrator looks up the subtask template, reads its `task_type`, and dispatches to the matching plugin.

`Register` returns an error if you double-register the same key. The registry is concurrency-safe.

If you don't want HTTP dispatch, pass `nil` and the built-in plugins fall back to `plugins.DefaultHTTPDispatcher`. For tests or fully local demos, pass your own `Dispatcher` (`func(ctx, url, taskID, payload) error`).

To write your own plugin, see [`plugin-author-guide.md`](plugin-author-guide.md).

---

## 4. Wiring the Temporal manager

The orchestrator depends on `engine.TemporalManager` from [`go-temporal-workflow`](https://github.com/OpenNSW/go-temporal-workflow). You need **two** instances: one for the parent workflow queue and one for the task workflow queue.

```go
parentWorkflowManager := engine.NewTemporalManager(
    temporalClient,
    "your-parent-queue",
    parentTaskHandler,        // called when a parent workflow hits a TASK node
    parentCompletionHandler,  // called when a parent workflow ends
)

taskWorkflowManager := engine.NewTemporalManager(
    temporalClient,
    "your-task-queue",
    taskHandler,              // called when a task workflow activates a SUBTASK node
    taskCompletionHandler,    // called when a task workflow ends
)
```

The handlers wire into the orchestrator:

```go
parentTaskHandler := func(p engine.TaskPayload) (map[string]any, error) {
    return tm.StartTask(p)
}

taskHandler := func(p engine.TaskPayload) (map[string]any, error) {
    return tm.StartSubTask(p)
}

taskCompletionHandler := func(workflowID string, vars map[string]any) error {
    return tm.HandleTaskCompletion(context.Background(), workflowID, vars)
}
```

`parentCompletionHandler` is yours alone — the orchestrator doesn't need to be told when the parent journey ends.

### The chicken-and-egg

`taskWorkflowManager` is a constructor argument to `NewTaskManager`, but `taskHandler` (which `taskWorkflowManager` needs) calls into `tm`. The demo resolves this by declaring `var tm *orchestrator.TaskManager` early and assigning it after both managers are built:

```go
var tm *orchestrator.TaskManager

taskHandler := func(p engine.TaskPayload) (map[string]any, error) {
    if tm == nil {
        return nil, fmt.Errorf("task manager not initialised")
    }
    return tm.StartSubTask(p)
}

// ... construct managers ...

tm = orchestrator.NewTaskManager(db, registry, pluginsReg, taskWorkflowManager, onTaskCompleted, rdr)
```

---

## 5. The `onTaskCompleted` callback

```go
type TaskCompletedCallback func(parentWorkflowID, parentRunID, parentNodeID string, finalVariables map[string]any) error
```

Fires when a task workflow ends. The library hands you the parent coordinates it recorded at `StartTask` time, plus the final variables from the task workflow. **Your job is to resume the parked parent activity:**

```go
onTaskCompleted := func(parentWorkflowID, parentRunID, parentNodeID string, vars map[string]any) error {
    return parentWorkflowManager.TaskDone(
        context.Background(),
        parentWorkflowID,
        parentRunID,
        parentNodeID,
        vars,
    )
}
```

If you return an error, the orchestrator logs it and the parent workflow stays parked. Make this idempotent — Temporal may retry.

---

## 6. The `Renderer`

```go
type Renderer interface {
    Render(ctx context.Context, config json.RawMessage, facts Facts) (json.RawMessage, error)
}
```

The renderer turns a snapshotted render config + the task's current `(state, data)` into a `json.RawMessage` (e.g. a map of UI slot → component, a custom layout config, or schema). The library is deliberately agnostic about what your config or UI representation looks like — `Render` receives raw JSON and decides what to do.

`demo/renderer.go` is a minimal state-keyed renderer: the config is `{state: {slot: component}}` and it picks the entry matching the current `State` (falling back to a `default` entry). Most real consumers will want something richer — templated payloads, schema lookups, role-based slot selection.

The render config is snapshotted into the `TaskRecord` at `StartTask` time. Once a task is running, mutating the registry entry won't affect it.

---

## Error semantics

The orchestrator uses two sentinel errors to signal "this isn't really an error, the workflow should park":

| Sentinel                                                         | Returned by                                        | Meaning                                                                                  |
|------------------------------------------------------------------|----------------------------------------------------|------------------------------------------------------------------------------------------|
| `activity.ErrResultPending` (from `go.temporal.io/sdk/activity`) | `StartTask`, `StartSubTask` (when plugin suspends) | The Temporal activity should park indefinitely; the orchestrator will resume it later.   |
| `plugins.ErrSuspended`                                           | A plugin's `Execute` method                        | "I dispatched the work; don't advance the workflow until something external resumes me." |

`StartSubTask` translates `ErrSuspended` from a plugin into `ErrResultPending` for Temporal. Both are normal happy-path values, not failures.

A real error from `StartTask` / `StartSubTask` (template not found, plugin failed) is logged and returned to Temporal, which will retry per your workflow's retry policy.

---

## What `StartTask` does, step by step

```go
func (tm *TaskManager) StartTask(payload engine.TaskPayload) (map[string]any, error) {
    // 1. Resolve TaskTemplate → workflow definition → render config
    // 2. Reject if the child workflow has parallel gateways
    // 3. taskID := payload.NodeID; taskWorkflowID := "task-wf-" + taskID
    // 4. Build the initial Data map from payload.Inputs (setNestedKey for dotted keys)
    // 5. Persist TaskRecord (state=STARTING, parent coords, render config snapshot)
    // 6. Start the child task workflow on the task queue
    // 7. Return activity.ErrResultPending — the parent activity parks
}
```

Note that **the parent activity returns `ErrResultPending` even on success**. The parent workflow doesn't get its result until `onTaskCompleted` wakes it later.

## What `CompleteTaskStep` does

```go
func (tm *TaskManager) CompleteTaskStep(ctx context.Context, taskID string, payload map[string]any) error {
    // 1. Load TaskRecord by TaskID — fails if missing or already COMPLETED
    // 2. Merge payload into record.Data (top-level keys, last-write-wins)
    // 3. SaveTask
    // 4. TemporalManager.TaskDone(ctx, TaskWorkflowID, TaskRunID, SubTaskNodeID, Data)
    //    → wakes the parked subtask activity with the full data map as its result
}
```

The portal calls this with whatever payload makes sense for the current subtask. The shape is typed by your subtask templates and plugins, not by the orchestrator.

---

## Production-readiness checklist

Things the demo cuts corners on, that you'll want for a real deployment:

- **Durable `TaskStore`.** The demo writes JSON to `/tmp`. Use Postgres / Spanner / DynamoDB.
- **Idempotent `onTaskCompleted`.** Temporal retries; you must tolerate replay.
- **Observability.** Plugins log via `log.Printf`. Replace with your structured logger; surface task state transitions, plugin errors, and callback failures to your observability stack.
- **`TaskID` uniqueness contract.** Node IDs in parent workflows must be globally unique across all running parent workflow instances. Establish a naming convention (e.g. `{workflowID}:{nodeID}`) or generate node IDs accordingly.
- **Plugin error handling.** Decide what should retry vs. fail-fast. The orchestrator currently surfaces non-`ErrSuspended` plugin errors to Temporal, which will retry per the activity's retry policy.
- **Authorisation.** `CompleteTaskStep` accepts any payload for any known task. Add authn/authz at your HTTP layer.

---

## See also

- [`architecture.md`](architecture.md) — the conceptual model
- [`frontend-guide.md`](frontend-guide.md) — what your portal calls
- [`plugin-author-guide.md`](plugin-author-guide.md) — extending behaviour
- [`template-reference.md`](template-reference.md) — the JSON shapes