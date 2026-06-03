# Frontend Guide

How a portal / UI consumes the task orchestrator over HTTP.

> Prerequisite: read [`architecture.md`](architecture.md) for the parent / task / subtask layering and the `TaskRecord` model.

---

## The mental model

For a UI integrator, three things are worth internalising:

1. **A task is addressable by its parent workflow's node ID.** When the user navigates to "the application-submission step of workflow X", you already know the task identifier — you don't need to call a discovery endpoint first.
2. **The current `State` field drives what to render.** The orchestrator's render config maps states to UI components. Your job is to fetch a task and display whatever `RenderResult` slots the server hands you.
3. **Submitting is one call.** `POST /api/task/{taskID}` with whatever payload the current state expects. The orchestrator figures out which subtask is parked and resumes it.

You never need to know about Temporal, runs, nodes, plugins, or workflows.

---

## The four endpoints

The demo (`demo/server.go`) ships a minimal HTTP surface that maps each `TaskManager` method to one route. A real integration can mirror these or rename them — the shapes are what matter.

| Verb / path                                    | Maps to                                    | Purpose                                            |
|------------------------------------------------|--------------------------------------------|----------------------------------------------------|
| `GET /api/tasks`                               | `GetAllTasks(ctx, "")`                     | List every task (summary, no render)               |
| `GET /api/tasks?parent_workflow_id=X`          | `GetAllTasks(ctx, "X")`                    | List tasks under a specific parent workflow        |
| *(not in demo today)* `GET /api/task/{taskID}` | `GetTaskRenderInfo(ctx, taskID)`           | Fetch one task **with its rendered view**          |
| `POST /api/task/{taskID}`                      | `CompleteTaskStep(ctx, taskID, payload)`   | Submit interaction data, resume the parked subtask |
| `POST /api/start`                              | `parentWorkflowManager.StartWorkflow(...)` | Kick off a new parent workflow (demo-specific)     |

> The demo currently exposes listing and completion. If you need single-task detail, add a thin handler around `GetTaskRenderInfo` — it's a single line.

---

## Response shapes

### `TaskView` (list — summary)

`GET /api/tasks` returns `[]TaskView` **without** the `View` field populated:

```json
[
  {
    "task_id": "submit_application",
    "task_type": "APPLICATION",
    "state": "PENDING_USER",
    "created_at": "2026-05-18T09:14:11Z",
    "updated_at": "2026-05-18T09:14:11Z"
  }
]
```

The `view` key is omitted (the JSON tag is `omitempty`). Listing is intentionally cheap — no render work is done for the elements you'll never display.

### `TaskView` (detail — with rendered view)

`GetTaskRenderInfo(ctx, taskID)` returns the same shape **with** `view` populated:

```json
{
  "task_id": "submit_application",
  "task_type": "APPLICATION",
  "state": "PENDING_USER",
  "view": {
    "primary": {
      "type": "markdown",
      "payload": "Please complete the applicant form."
    }
  },
  "created_at": "2026-05-18T09:14:11Z",
  "updated_at": "2026-05-18T09:14:11Z"
}
```

### `View` JSON structure

The `view` field returned in the API is a raw JSON payload (`json.RawMessage`). The core orchestrator does not enforce any specific schema, allowing you to use whatever layout paradigm fits your frontend.

However, the standard demo implementation (`SimpleRenderer`) uses a **Slot/Component** layout convention:

```go
type RenderResult map[string]UIComponent

type UIComponent struct {
    Type    string          `json:"type"`    // "markdown", "jsonforms", "data_table", …
    Payload json.RawMessage `json:"payload"` // component-specific
}
```

In this convention, `view` is a map from **slot name** (e.g. `"primary"`, `"sidebar"`, `"action_panel"`) to a component spec. The frontend's job is to:
1. Parse the raw `view` JSON.
2. For each slot, look up the appropriate UI widget to display based on its `type`.
3. Render the widget using the corresponding `payload`.

---

## The standard interaction loop

```mermaid
sequenceDiagram
    participant FE as Frontend
    participant API as Orchestrator API

    FE->>API: GET /api/tasks?parent_workflow_id=phyto-123
    API-->>FE: [{ task_id, state, ... }, ...]

    Note over FE: User picks a task (or you have its ID from routing)

    FE->>API: GET /api/task/submit_application
    API-->>FE: { state, view: { primary: { type: "jsonforms", payload: {schema, uischema, data} } } }

    Note over FE: Render the view's slots; user fills the form

    FE->>API: POST /api/task/submit_application
    Note right of FE: { ...form data... }   ← raw payload, server stamps the slot
    API-->>FE: 200 OK

    Note over FE: Re-fetch — state may now be QUEUED_EXTERNALLY, PENDING_PAYMENT, etc.

    FE->>API: GET /api/task/submit_application
    API-->>FE: { state: "QUEUED_EXTERNALLY", view: { primary: { ... new payload ... } } }
```

### When to refresh

After any `POST /api/task/{taskID}`, the task either:

- **Advances to a new subtask** (e.g. `PENDING_USER` → `QUEUED_EXTERNALLY`) — re-fetch to get the new view.
- **Completes the task** (state → `COMPLETED`) — re-fetch to render the terminal state.
- **Stays put** (the next subtask is the same kind) — re-fetch anyway; the data may have changed.

A simple rule: **always re-fetch after a successful POST**. Skip optimistic updates unless your UX requires them.

### Polling

For asynchronous transitions driven by external systems (an external reviewer approving, a payment webhook arriving), the task's state changes without the frontend doing anything. Poll `GET /api/task/{taskID}` on an interval (5–10s is reasonable for most cases) or use server-sent events / websockets in front of it if you need lower latency.

---

## Submitting interaction data

The `POST /api/task/{taskID}` body is **the raw form payload** — what the current subtask's plugin expects, unwrapped. The server places the whole object into a single top-level slot of `TaskRecord.Data` determined by the active `SubTaskTemplate.OutputNamespace`.

```jsonc
// User form submission — no wrapper.
{
  "applicant_name": "Alice",
  "email": "alice@example.com",
  "items": [...]
}

// Reviewer decision — no wrapper.
{
  "decision": "approved",
  "notes": "Looks good."
}
```

If the user-input subtask's template declares `"output_namespace": "userform"`, the orchestrator persists this as `record.Data["userform"] = { ...payload... }`. Subsequent subtasks read it back via the namespaced path. **The frontend doesn't choose the namespace** — the template author does, and it travels with the active subtask snapshot on the record.

> Pre-2026: callers wrapped their payload manually (`{"userform": {...}}`). That's no longer accepted; the FE now sends the inner object directly.

### What happens if the active subtask has no `output_namespace`

The server **logs a warning and drops the payload**, then resumes the workflow as if the body were empty. This is intentional: a misconfigured template shouldn't break a running task, but data must never land in unscoped top-level keys (that would let a caller overwrite slots owned by other subtasks). If you see a submission silently fail to appear in the next render, check that the active subtask's template declares an output namespace.

### Empty bodies

A `POST` with an empty body (or no JSON object) is valid — it acts as a **resume signal** with no data attached. Useful for wait-for-event subtasks where the resume itself is the event.

### Errors

| Status                      | Reason                                                                        |
|-----------------------------|-------------------------------------------------------------------------------|
| `404 Not Found`             | No task with that `taskID`                                                    |
| `409 Conflict`              | Task is already `COMPLETED` (can't resume a finished task)                    |
| `400 Bad Request`           | Malformed JSON or missing path param                                          |
| `500 Internal Server Error` | Temporal couldn't resume the workflow (plugin failure, transient infra error) |

The demo maps these from `manager.CompleteTaskStep` error text. Expect to see the same shape unless your service customises it.

---

## State-driven rendering patterns

`State` is the primary input to your rendering logic. Common states from the built-in plugins:

| State               | What's happening                          | Typical UI                                    |
|---------------------|-------------------------------------------|-----------------------------------------------|
| `STARTING`          | Task workflow is initialising             | Spinner, "Starting your application…"         |
| `PENDING_USER`      | A form-style subtask is waiting for input | Render the form, expose submit                |
| `QUEUED_EXTERNALLY` | Dispatched to an external system, waiting | Status banner, no input                       |
| `PENDING_PAYMENT`   | Payment subtask is awaiting confirmation  | Payment widget                                |
| `DISPATCHED`        | Fire-and-forget completed (no resume)     | Status, possibly auto-advance                 |
| `COMPLETED`         | Task workflow finished                    | Terminal screen, link back to parent workflow |

These are not magic — they're strings written by plugins. Your application can define and use its own. The renderer is what gives them meaning.

---

## What you don't see

By design, these are hidden from the frontend:

- **Temporal coordinates** — `TaskWorkflowID`, `TaskRunID`, `SubTaskNodeID`, etc. They're internal to the orchestrator and never serialised in `TaskView`.
- **Render config** — the JSON snapshot in `TaskRecord.RenderConfig` is server-side only; the FE sees the rendered output, not the template.
- **Which plugin handled a step** — irrelevant to the UI.
- **Workflow / task definitions** — also internal.

If the FE thinks it needs one of these, that's usually a signal the responsibility belongs on the server side.

---

## See also

- [`architecture.md`](architecture.md) — what's going on behind the API
- [`integration-guide.md`](integration-guide.md) — for the team running the server
- [`template-reference.md`](template-reference.md) — to understand where `view` payloads come from