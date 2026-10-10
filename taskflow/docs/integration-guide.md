# Integration Guide

How to embed the orchestrator in your own Go service.

> Prerequisite: read [`architecture.md`](architecture.md) first. This guide assumes you understand the Parent / Task / Step layering and the `TaskRecord` shape.

---

## What you're building

To run the orchestrator, you provide concrete implementations of four dependencies and one callback:

| Dependency        | Interface                            | What it does                                                   |
|-------------------|--------------------------------------|----------------------------------------------------------------|
| Task store        | `store.TaskStore`                    | Persists `TaskRecord`s                                         |
| Template registry | `orchestrator.TaskTemplateRegistry`  | Looks up task / step / workflow / render-config definitions |
| Plugin registry   | `*plugins.Registry` (concrete)       | Maps `TaskType` → plugin handler                               |
| Temporal manager  | `engine.TemporalManager`             | Starts task workflows and resumes parked activities            |
| Renderer          | `renderer.Renderer`                  | Turns `(state, data)` + render config → a UI view              |
| Callback          | `orchestrator.TaskCompletedCallback` | Wakes the parent workflow when a task finishes                 |

Then you call:

```go
tm := orchestrator.NewTaskManager(db, registry, pluginsReg, taskWorkflowManager, onTaskCompleted, rdr)
```

…and wire `tm.StartTask`, `tm.StartTaskStep`, and `tm.HandleTaskCompletion` into your Temporal handlers. `demo/main.go` is the complete worked example.

---

## 1. Implementing `TaskStore`

```go
type TaskStore interface {
    InitTask(ctx context.Context, record TaskRecord)
    GetTask(ctx context.Context, taskID string) (TaskRecord, bool)
    GetTaskByWorkflowID(ctx context.Context, workflowID string) (TaskRecord, bool)
    GetAllTasks(ctx context.Context, parentWorkflowID string) []TaskRecord

    // Guarded step writes: one atomic conditional statement each, returning the rows changed.
    ClaimStep(ctx context.Context, taskID string, claim StepClaim) (int64, error)
    WriteRenderState(ctx context.Context, taskID, stepID string, seq int64, state string, data map[string]any) (int64, error)
    PersistSubmission(ctx context.Context, taskID, stepID string, seq int64, data map[string]any) (int64, error)
    CompleteTask(ctx context.Context, taskID string, seq int64) (int64, error)

    // Guarded claim writes: the same kind of statement, on the claim columns only.
    ClaimTask(ctx context.Context, taskID, holder string, at time.Time) (int64, error)
    ReleaseTask(ctx context.Context, taskID, holder string) (int64, error)
}
```

**Contracts:**

- **`InitTask` is an upsert** keyed on `record.TaskID`. It creates the row (`StartTask`) and writes the coarse fields. It **must not write `ActiveStepID` or `Seq`**: those belong to the guarded statements, and a stale full-record save would move them backwards. The signature returns no error; if your store can fail, log and surface failures via your own observability.
- **`GetTask`** looks up by `TaskID` (the parent's step ID — see the architecture doc). Must return `(zero, false)` when absent, never panic.
- **`GetTaskByWorkflowID`** looks up by `TaskWorkflowID` (the child workflow's Temporal ID). Used internally by `StartTaskStep` and `HandleTaskCompletion`. It must scan or index by that field, not by `TaskID`.
- **`GetAllTasks`** returns every record if `parentWorkflowID == ""`, otherwise only records where `record.ParentWorkflowID == parentWorkflowID`. Used by the portal listing API.
- **The four step methods** must each be a single atomic conditional write, with the guard inside the statement — not a read followed by a write:

  | Method              | Guard                                | Sets                                                                        |
  |---------------------|--------------------------------------|-----------------------------------------------------------------------------|
  | `ClaimStep`         | stored `seq <= claim.Seq`            | `active_step_id`, `seq`, `active_task_template_id`, `state`, `data` (replaced, not merged) |
  | `WriteRenderState`  | `active_step_id = stepID AND seq = seq` | `state`, `data`                                                          |
  | `PersistSubmission` | `active_step_id = stepID AND seq = seq` | `data`, `state = ADVANCING`, `seq = seq + 1`                             |
  | `CompleteTask`      | stored `seq <= seq`                  | `state = COMPLETED`, `seq`                                                  |

  Return the number of rows changed. **0 rows means the write was stale and was dropped; it is not an error.** Return an error only when the store failed.
- **The two claim methods** are conditional writes of the same kind, on `claimed_by` and `claimed_at` only. They must not change `seq` or the step columns, and the step methods and `InitTask` must not change the claim columns:

  | Method        | Guard                                                              | Sets                                                                         |
  |---------------|--------------------------------------------------------------------|------------------------------------------------------------------------------|
  | `ClaimTask`   | (`claimed_by` is NULL or `holder`) and `state` is not `COMPLETED` | `claimed_by = holder`; `claimed_at = at`, unless `holder` already had the claim |
  | `ReleaseTask` | `claimed_by = holder`                                              | `claimed_by` and `claimed_at` to NULL                                        |

  Store an unclaimed task as NULL, not `""`, so the guard's `IS NULL` matches it. See [Task claims](#7-task-claims) for what the manager builds on these.

A GORM implementation is in `store/gorm`. Its table needs, beyond the original columns:

```sql
ALTER TABLE task_records_v2 ADD COLUMN IF NOT EXISTS active_step_id UUID NULL;
ALTER TABLE task_records_v2 ADD COLUMN IF NOT EXISTS seq BIGINT NOT NULL DEFAULT 0;
ALTER TABLE task_records_v2 ADD COLUMN IF NOT EXISTS claimed_by TEXT NULL;
ALTER TABLE task_records_v2 ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMPTZ NULL;
```

`InitTask` leaves the claim columns out of its insert, so tasks can still be created before they exist. Add them before anything calls `ClaimTask` or `ReleaseTask`.

### Concurrency

The store is called from multiple goroutines (Temporal worker pool, HTTP handlers). Your implementation must be safe for concurrent use. The demo uses a `sync.RWMutex`; a SQL-backed store gets this from the DB. The conditional writes are what make racing callers, retried activities and late attempts safe, so they must really be atomic.

### What "updated" means

Plugins mutate the in-memory `TaskRecord` pointer they receive in `PluginContext.Record`; the orchestrator persists the result with `WriteRenderState`, guarded so a step that is no longer the active one cannot write. You don't need to diff or track changes.

---

## 2. Implementing `TaskTemplateRegistry`

```go
type TaskTemplateRegistry interface {
    GetTaskTemplate(id string) (TaskTemplate, bool)
    GetStepTemplate(id string) (StepTemplate, bool)
    GetWorkflow(id string) (engine.WorkflowDefinition, bool)
    GetGenericTemplate(id string) (json.RawMessage, bool)
}
```

This is read-only from the orchestrator's perspective. How definitions get *into* your registry is your call — load from disk at startup (the demo does this with `demo/templates/*.json`), pull from a config service, hard-code them, etc.

| Method               | Resolves                                          | Used in                                                  |
|----------------------|---------------------------------------------------|----------------------------------------------------------|
| `GetTaskTemplate`    | `payload.TaskTemplateID` from the parent workflow | `StartTask`                                              |
| `GetStepTemplate` | `payload.TaskTemplateID` from the child workflow  | `StartTaskStep`                                           |
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

The key — `"USER_INPUT"`, `"PAYMENT"`, etc. — is the `task_type` field in the **StepTemplate** JSON. When the task workflow activates a step node, the orchestrator looks up the step template, reads its `task_type`, and dispatches to the matching plugin.

`Register` returns an error if you double-register the same key. The registry is concurrency-safe.

If you don't want HTTP dispatch, pass `nil` and the built-in plugins fall back to `plugins.DefaultHTTPDispatcher`. For tests or fully local demos, pass your own `Dispatcher` (`func(ctx, url, taskID, payload) error`).

To write your own plugin, see [`plugin-author-guide.md`](plugin-author-guide.md).

---

## 4. Wiring the Temporal manager

The orchestrator depends on `engine.TemporalManager` from [`github.com/OpenNSW/core/workflow`](../../workflow) (imported as `engine`). You need **two** instances: one for the parent workflow queue and one for the task workflow queue.

```go
parentWorkflowManager := engine.NewTemporalManager(
    temporalClient,
    "default",                // must match the namespace temporalClient was dialed with
    "your-parent-queue",
    parentTaskHandler,        // called when a parent workflow hits a TASK node
    parentCompletionHandler,  // called when a parent workflow ends
)

taskWorkflowManager := engine.NewTemporalManager(
    temporalClient,
    "default",
    "your-task-queue",
    taskHandler,              // called when a task workflow activates a TASK node
    taskCompletionHandler,    // called when a task workflow ends
)
```

The handlers wire into the orchestrator:

```go
parentTaskHandler := func(p engine.TaskPayload) (map[string]any, error) {
    return tm.StartTask(p)
}

taskHandler := func(p engine.TaskPayload) (map[string]any, error) {
    return tm.StartTaskStep(p)
}

taskCompletionHandler := func(c engine.WorkflowCompletion) error {
    return tm.HandleTaskCompletion(context.Background(), c)
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
    return tm.StartTaskStep(p)
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
    return parentWorkflowManager.CompleteActivation(
        context.Background(),
        parentWorkflowID,
        parentRunID,
        parentNodeID,
        vars,
    )
}
```

If you return an error, the orchestrator logs it, returns it to Temporal, and Temporal retries the whole completion, so the callback must tolerate being called again. **Return `CompleteActivation`'s error as it is:** when the parent's step is no longer pending, `CompleteActivation` returns `engine.ErrActivationNotPending`, and `HandleTaskCompletion` treats that as "an earlier attempt already woke the parent", which is success. `parentStepID` is the parent's **step ID**, which is what `CompleteActivation` takes.

---

## 6. The `Renderer`

```go
type Renderer interface {
    Render(ctx context.Context, config json.RawMessage, facts Facts) (json.RawMessage, error)
}
```

The renderer turns a snapshotted render config + the task's current `(state, data)` into a `json.RawMessage` (e.g. a map of UI slot → component, a custom layout config, or schema). The library is deliberately agnostic about what your config or UI representation looks like — `Render` receives raw JSON and decides what to do.

`Facts` also carries `Claims map[string]bool` — authorization decisions the *caller* resolved before rendering, which a renderer may use to decide what this particular caller is allowed to see. taskflow makes no policy decision of its own; it only forwards them. Callers supply them per request through `ZoneViewAssembler.Assemble(ctx, record, claims)` and pass `nil` when their render configs gate nothing on a claim.

`demo/renderer.go` is a minimal state-keyed renderer: the config is `{state: {slot: component}}` and it picks the entry matching the current `State` (falling back to a `default` entry). Most real consumers will want something richer — templated payloads, schema lookups, role-based slot selection.

The render config is snapshotted into the `TaskRecord` at `StartTask` time. Once a task is running, mutating the registry entry won't affect it.

---

## 7. Task claims

A claim records who is working a task, so two people don't act on it at once and everyone can see who has it. The split of work is:

- **taskflow stores claims.** `TaskManager.ClaimTask(ctx, taskID, holder)` and `ReleaseTask(ctx, taskID, holder)` are atomic, and task views carry `claimed_by` and `claimed_at`. taskflow never decides who may claim a task or what a claim is required for.
- **Your host enforces them.** It exposes claim and release endpoints, chooses the holder value, and decides which actions need the claim.

The rules taskflow applies:

- A claim succeeds when the task is unclaimed or already claimed by `holder`. A repeat claim keeps the original `claimed_at`.
- A completed task cannot be claimed. A claim held when it completed is kept, as a record of who worked it, and can still be released.
- Only the holder can release. Releasing an unclaimed task succeeds, so a repeated release is harmless.
- A claim never changes the task's step or version, so it does not make an open view stale.
- The claim is on the task row, so it lasts for the whole task, across every step inside it. A loop **inside** the task workflow (for example, sending a form back for more information) keeps the claim. A loop in the **parent** workflow starts a new task, which starts unclaimed.

What your host needs to do:

1. **Choose the holder value.** Use a stable, internal user ID, not an email or a display name. taskflow treats it as opaque. Resolve it to a name when you build responses.
2. **Expose claim and release** for callers who can see the task, and map the errors (below).
3. **Enforce the claim where your policy needs it.** For example, before calling `CompleteTaskStep`, reject a user who does not hold the claim. Exempt machine callers such as `CompleteTaskStepByToken`. The check and the submit are two calls, but that gap is harmless: the workflow still accepts only one submission for a step.
4. **Optionally gate the view on the claim.** Pass a fact such as `claim:mine` in the claims you give the renderer, and use it in `visibleWhen.requireClaim` to hide actions from someone who does not hold the claim. The renderer rejects a render config that references a fact you did not pass, so pass every claim fact your configs use on every read, with `false` for the ones that don't hold.

| Error                      | Returned by                    | HTTP |
|----------------------------|--------------------------------|------|
| `ErrHolderRequired`        | `ClaimTask`, `ReleaseTask`     | 400  |
| `ErrTaskNotFound`          | `ClaimTask`, `ReleaseTask`     | 404  |
| `*ClaimHeldError` (matches `ErrClaimHeld`) | `ClaimTask` | 409; its `Holder` says who has the claim |
| `ErrTaskCompleted`         | `ClaimTask`                    | 409  |
| `ErrNotClaimHolder`        | `ReleaseTask`                  | 403  |

"Claim" here is not `TaskStore.ClaimStep`, which makes a step the task's active step, and not the claims passed to the renderer (`renderer.Facts.Claims`), which are authorization facts such as `role:officer`.

---

## Error semantics

`ErrStaleStep` and `ErrStepIDRequired` (see `CompleteTaskStep`) are for your HTTP layer to map to 409 and 400. `ErrTaskNotFound`, from `CompleteTaskStep`, `GetTaskRenderInfo` and the claim methods, maps to 404. The claim errors are listed in [Task claims](#7-task-claims). `engine.ErrActivationNotPending` is what `CompleteActivation` returns for a step that already completed or never existed.

The orchestrator uses two sentinel errors to signal "this isn't really an error, the workflow should park":

| Sentinel                                                         | Returned by                                        | Meaning                                                                                  |
|------------------------------------------------------------------|----------------------------------------------------|------------------------------------------------------------------------------------------|
| `activity.ErrResultPending` (from `go.temporal.io/sdk/activity`) | `StartTask`, `StartTaskStep` (when plugin suspends) | The Temporal activity should park indefinitely; the orchestrator will resume it later.   |
| `plugins.ErrSuspended`                                           | A plugin's `Execute` method                        | "I dispatched the work; don't advance the workflow until something external resumes me." |

`StartTaskStep` translates `ErrSuspended` from a plugin into `ErrResultPending` for Temporal. Both are normal happy-path values, not failures.

A real error from `StartTask` / `StartTaskStep` (template not found, plugin failed) is logged and returned to Temporal, which will retry per your workflow's retry policy.

---

## What `StartTask` does, step by step

```go
func (tm *TaskManager) StartTask(payload engine.TaskPayload) (map[string]any, error) {
    // 1. Resolve TaskTemplate → workflow definition → render config
    // 2. Reject if the child workflow has parallel gateways
    // 3. taskID := payload.ActivationID (the parent's step ID); taskWorkflowID := "task-wf-" + taskID
    // 4. Build the initial Data map from payload.Inputs (setNestedKey for dotted keys)
    // 5. Persist TaskRecord (state=STARTING, parent coords, render config snapshot)
    // 6. Start the child task workflow on the task queue
    // 7. Return activity.ErrResultPending — the parent activity parks
}
```

Note that **the parent activity returns `ErrResultPending` even on success**. The parent workflow doesn't get its result until `onTaskCompleted` wakes it later.

## What `StartTaskStep` does

```go
func (tm *TaskManager) StartTaskStep(ctx context.Context, payload engine.TaskPayload) (map[string]any, error) {
    // 1. Load the task by its workflow ID, the template, and the plugin
    // 2. Claim the step: ClaimStep(stepID, seq), guarded by seq.
    //    0 rows → a late or duplicate attempt: return ErrStaleStep (non-retryable) BEFORE the plugin runs
    // 3. Run the plugin on an in-memory copy of the record (it may dispatch externally)
    // 4. WriteRenderState, guarded by the active step and seq; 0 rows is success
    // 5. ErrSuspended → activity.ErrResultPending; otherwise return the data (sync completion)
}
```

## What `CompleteTaskStep` does

```go
func (tm *TaskManager) CompleteTaskStep(ctx context.Context, taskID, stepID string, payload map[string]any) error {
    // 0. stepID is required (ErrStepIDRequired → 400)
    // 1. Pre-check the row: task not completed and stepID is the active step (else ErrStaleStep → 409)
    // 2. Pre-resume extensions on copies of the record and payload (authz, validation)
    // 3. TemporalManager.CompleteActivation(TaskWorkflowID, stepID, payload). Exactly one caller wins;
    //    a step that is not pending fails with ErrStaleStep and nothing is written
    // 4. PersistSubmission (state ADVANCING, seq+1), guarded; 0 rows is success
    // 5. Post-resume extensions in the background, best effort
}
```

The portal calls this with the `step_id` its task view reported and whatever payload makes sense for the current step. The shape is typed by your step templates and plugins, not by the orchestrator. A nil return means the workflow **accepted** the step; the row shows `ADVANCING` (or the next step) from then on, so clients refetch instead of assuming the state. Map `ErrStepIDRequired` to 400 and `ErrStaleStep` to 409.

An external system that calls back is addressed by the **callback token** the dispatch carried in `X-Task-ID` (see `plugins.CallbackToken`): call `CompleteTaskStepByToken(ctx, token, payload)`. A malformed token returns an error wrapping `callbacktoken.ErrInvalid` (400). The receiver should also use the token as its idempotency key: it is the same on every retry of one step's dispatch and different for every step.

## What `HandleTaskCompletion` does

```go
func (tm *TaskManager) HandleTaskCompletion(ctx context.Context, c engine.WorkflowCompletion) error {
    // 1. CompleteTask(taskID, c.Seq): COMPLETED, guarded so it only matches if no later step exists
    // 2. Then wake the parent through onTaskCompleted.
    //    engine.ErrActivationNotPending from it means an earlier attempt already woke the parent: success
}
```

The completion is written first and each half is safe to repeat, so a crash between them is repaired by Temporal retrying the call. A completed row does not short-circuit the retry.

---

## Production-readiness checklist

Things the demo cuts corners on, that you'll want for a real deployment:

- **Durable `TaskStore`.** The demo writes JSON to `/tmp`. Use Postgres / Spanner / DynamoDB.
- **Idempotent `onTaskCompleted`.** Temporal retries; you must tolerate replay.
- **Observability.** Plugins log via `log.Printf`. Replace with your structured logger; surface task state transitions, plugin errors, and callback failures to your observability stack.
- **Schema.** Add `active_step_id` and `seq` to your task table (section 1) before deploying a version that uses the guarded writes, and `claimed_by` and `claimed_at` before using task claims.
- **Plugin error handling.** Decide what should retry vs. fail-fast. The orchestrator currently surfaces non-`ErrSuspended` plugin errors to Temporal, which will retry per the activity's retry policy.
- **Authorisation.** `CompleteTaskStep` accepts any payload for any known task. Add authn/authz at your HTTP layer, including any claim requirement (section 7).

---

## See also

- [`architecture.md`](architecture.md) — the conceptual model
- [`frontend-guide.md`](frontend-guide.md) — what your portal calls
- [`plugin-author-guide.md`](plugin-author-guide.md) — extending behaviour
- [`template-reference.md`](template-reference.md) — the JSON shapes