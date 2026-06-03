# NSW Task Flow Engine

[![Go Reference](https://pkg.go.dev/badge/github.com/OpenNSW/nsw-task-flow.svg)](https://pkg.go.dev/github.com/OpenNSW/nsw-task-flow)
[![Go Test](https://github.com/OpenNSW/nsw-task-flow/actions/workflows/go.yml/badge.svg)](https://github.com/OpenNSW/nsw-task-flow/actions)

A modular, domain-driven task orchestration library for Go, designed to sit between a macro business workflow engine (Temporal-based) and the micro interactive flows it activates.

It separates **long-running business journeys** ("submit a phyto application") from the **interactive steps** that fulfil them ("fill this form", "wait for reviewer approval", "process payment"), giving you a clean integration boundary for portal-style applications.

---

## Conceptual model

The system has three layers, each with a single, focused responsibility:

```
              [Parent Workflow]     ← macro business journey
                     │
                     ▼  StartTask
              [TaskManager] ─────► [TaskRecord in DB]
                     │
                     ▼  StartTaskWorkflow
              [Task Workflow]       ← micro interactive journey
                     │
                     ▼  StartSubTask
              [SubTask Node]        ← single interaction step (form, API call, payment, …)
                     │
                     ▼  CompleteTaskStep
            [Resume & Continue]
                     │
                     ▼  Task workflow ends
            [HandleTaskCompletion]
                     │
                     ▼  onTaskCompleted callback
              [Resume Parent Workflow]
```

- **Parent Workflow** — the macro business process. Has no knowledge of forms, payments, or external systems.
- **Task** — a self-contained micro-flow (its own Temporal workflow) that fulfils one parent step.
- **SubTask** — an individual node inside a Task workflow, executed by a registered **plugin** (`USER_INPUT`, `PAYMENT`, `EXTERNAL_REVIEW`, `FIRE_AND_FORGET`, or your own).

See [`docs/architecture.md`](docs/architecture.md) for the deep dive: lifecycle, state machine, sequence diagrams, and `TaskRecord` semantics.

---

## Public API at a glance

The `TaskManager` exposes four methods that span the integration surface:

| Method                                   | Called by                       | Purpose                                                           |
|------------------------------------------|---------------------------------|-------------------------------------------------------------------|
| `StartTask(payload)`                     | Parent workflow engine callback | Create a new task and start its child workflow                    |
| `GetAllTasks(ctx, parentWorkflowID)`     | Portal / UI                     | List tasks (lightweight summary; optional parent-workflow filter) |
| `GetTaskRenderInfo(ctx, taskID)`         | Portal / UI                     | Fetch a single task with its fully rendered view                  |
| `CompleteTaskStep(ctx, taskID, payload)` | Portal / UI                     | Submit interaction data and resume the active subtask             |

There are also two internal hooks invoked by the task workflow itself: `StartSubTask` (a workflow node activates) and `HandleTaskCompletion` (the task workflow finishes).

### Key invariants

- **`TaskID` is the parent workflow's `NodeID`.** No internal UUIDs — the task is addressable by an identifier the caller already knows.
- **No parallel subtasks.** A `TaskRecord` stores coordinates for one active subtask. Task workflows must be sequential.
- **`StartTask` returns `activity.ErrResultPending`** on the happy path. The task workflow runs asynchronously; the parent activity stays suspended until `onTaskCompleted` fires.
- **Plugins suspend by returning `plugins.ErrSuspended`.** Synchronous plugins return `nil`; the workflow proceeds without parking.
- **Submissions are scoped.** `CompleteTaskStep` payloads land in a single top-level slot of `Record.Data`, named by the active `SubTaskTemplate.OutputNamespace`. Callers send the raw form object — the server stamps the slot. Missing namespace → payload dropped with a warning, never an open top-level merge.

---

## Quick start: wire it up

```go
import (
    "context"

    engine "github.com/OpenNSW/go-temporal-workflow"
    "github.com/OpenNSW/nsw-task-flow/orchestrator"
    "github.com/OpenNSW/nsw-task-flow/plugins"
    "github.com/OpenNSW/nsw-task-flow/renderer"
    "github.com/OpenNSW/nsw-task-flow/store"
)

// 1. Bring your own implementations of TaskStore, TaskTemplateRegistry, and Renderer.
var db store.TaskStore               = myStore
var registry orchestrator.TaskTemplateRegistry = myRegistry
var rdr renderer.Renderer            = mySimpleRenderer

// 2. Register the plugins you need.
pluginsReg := plugins.NewRegistry()
pluginsReg.Register("USER_INPUT", plugins.NewUserInputPlugin())
pluginsReg.Register("FIRE_AND_FORGET", plugins.NewAPICallPlugin(nil))

// 3. Callback that wakes the parent workflow when a task completes.
onTaskCompleted := func(parentWorkflowID, parentRunID, parentNodeID string, vars map[string]any) error {
    return parentWorkflowManager.TaskDone(context.Background(), parentWorkflowID, parentRunID, parentNodeID, vars)
}

// 4. Build the manager.
tm := orchestrator.NewTaskManager(db, registry, pluginsReg, taskWorkflowManager, onTaskCompleted, rdr)

// 5. Hook it into your Temporal handlers.
parentTaskHandler := func(p engine.TaskPayload) (map[string]any, error) { return tm.StartTask(p) }
taskHandler       := func(p engine.TaskPayload) (map[string]any, error) { return tm.StartSubTask(p) }
taskCompletionHandler := func(wfID string, vars map[string]any) error {
    return tm.HandleTaskCompletion(context.Background(), wfID, vars)
}
```

`demo/main.go` is a working reference implementation of every dependency above.

---

## Where to go next

| Audience                                                                | Read                                                         |
|-------------------------------------------------------------------------|--------------------------------------------------------------|
| **Backend service** embedding the orchestrator                          | [`docs/integration-guide.md`](docs/integration-guide.md)     |
| **Frontend / portal** consuming the HTTP API                            | [`docs/frontend-guide.md`](docs/frontend-guide.md)           |
| **Plugin author** writing a new subtask handler                         | [`docs/plugin-author-guide.md`](docs/plugin-author-guide.md) |
| **Template author** defining tasks, subtasks, workflows, render configs | [`docs/template-reference.md`](docs/template-reference.md)   |
| **Anyone** wanting the conceptual model                                 | [`docs/architecture.md`](docs/architecture.md)               |

---

## Running the demo

The repository ships a self-contained demo that exercises the full stack (Parent Workflow → Task → User Input → External Review → Payment → Completion).

**Prerequisites**

- Go 1.20+
- [Temporal CLI](https://docs.temporal.io/cli/) for a local dev server

**Run**

```bash
# Terminal 1 — Temporal dev server
temporal server start-dev

# Terminal 2 — the demo
go run ./demo
```

Open <http://localhost:8080> and click **Start Workflow**. The split-pane UI shows the applicant on the left and the reviewer on the right, both driven by the same `TaskManager` instance.

---

## Project layout

```
orchestrator/     # TaskManager, TaskTemplateRegistry, TaskView
plugins/          # Plugin interface, Registry, built-in plugins
renderer/         # Renderer interface, RenderResult, UIComponent
store/            # TaskStore interface, TaskRecord
demo/             # Reference implementation: HTTP server, file-backed store, JSON templates
docs/             # Integration / frontend / plugin / template / architecture guides
```

---

## Testing

```bash
go test -race ./...
```

The core packages have integration coverage for state transitions, callback invocation, plugin suspension semantics, and the render pipeline.