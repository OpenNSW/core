# Template Reference

JSON shapes for everything you register with the orchestrator: task templates, subtask templates, workflow definitions, and render configs.

> Prerequisite: read [`architecture.md`](architecture.md). This document is the schema reference; the architecture doc explains what each piece is *for*.

---

## The four template kinds

| Kind                | Go type                                                   | Resolves via                                         | Used at                                     |
|---------------------|-----------------------------------------------------------|------------------------------------------------------|---------------------------------------------|
| Task template       | `orchestrator.TaskTemplate`                               | `payload.TaskTemplateID` (from parent workflow node) | `StartTask`                                 |
| SubTask template    | `orchestrator.SubTaskTemplate`                            | `payload.TaskTemplateID` (from task workflow node)   | `StartSubTask`                              |
| Workflow definition | `engine.WorkflowDefinition` (from `go-temporal-workflow`) | `TaskTemplate.WorkflowID`                            | `StartTask`                                 |
| Render config       | raw JSON                                                  | `TaskTemplate.RenderConfigID`                        | `StartTask` (snapshotted onto `TaskRecord`) |

All four live in your `TaskTemplateRegistry` implementation. How you load them — JSON on disk (the demo does this), a config service, a database — is your call.

---

## Task template

The macro definition of a task: which child workflow runs it, and which render config shapes its UI.

```go
type TaskTemplate struct {
    ID             string `json:"id"`
    Type           string `json:"type"`
    WorkflowID     string `json:"workflow_id"`
    RenderConfigID string `json:"render_config_id"`
}
```

| Field              | Required | Purpose                                                                                                                        |
|--------------------|----------|--------------------------------------------------------------------------------------------------------------------------------|
| `id`               | yes      | Unique within the registry. Matches `payload.TaskTemplateID` from the parent workflow.                                         |
| `type`             | yes      | User-facing category, surfaced as `TaskView.TaskType`. Convention: uppercase domain noun (`APPLICATION`, `REVIEW`, `RENEWAL`). |
| `workflow_id`      | yes      | Resolves to an `engine.WorkflowDefinition` — the child workflow this task runs.                                                |
| `render_config_id` | yes      | Resolves to a render config (raw JSON, snapshotted at `StartTask`).                                                            |

**Example** (`demo/templates/task_phyto_application_task.json`):

```json
{
  "id": "demo_phyto_application_task",
  "type": "APPLICATION",
  "workflow_id": "Phyto_Application_Flow_v1",
  "render_config_id": "phyto_render_config"
}
```

The parent workflow refers to this task by `id`. When the parent hits a TASK node carrying `task_template_id: "demo_phyto_application_task"`, the orchestrator:

1. Looks up this template.
2. Looks up the workflow definition `Phyto_Application_Flow_v1`.
3. Looks up render config `phyto_render_config` and snapshots its bytes into `TaskRecord.RenderConfig`.

---

## SubTask template

The definition of a single interaction step *inside* a task workflow.

```go
type SubTaskTemplate struct {
    ID               string          `json:"id"`
    TaskType         string          `json:"task_type"`
    PluginProperties json.RawMessage `json:"plugin_properties"`
    OutputNamespace  string          `json:"output_namespace,omitempty"`
}
```

| Field               | Required                | Purpose                                                                                                                                                                                               |
|---------------------|-------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `id`                | yes                     | Unique within the registry. Matches `payload.TaskTemplateID` from the task workflow node.                                                                                                             |
| `task_type`         | yes                     | **Plugin lookup key.** Must match the string used in `pluginsReg.Register(taskType, plugin)`.                                                                                                         |
| `plugin_properties` | varies                  | Free-form JSON passed verbatim to the plugin's `Execute`. Shape is owned by the plugin.                                                                                                               |
| `output_namespace`  | for submission subtasks | Top-level slot in `TaskRecord.Data` where `CompleteTaskStep` payloads are written. Required for any subtask that accepts an external submission. Without it, submitted payloads are dropped (logged). |

**Example** (`demo/templates/subtask_demo_generic_user_input.json`):

```json
{
  "id": "demo_generic_user_input",
  "task_type": "USER_INPUT",
  "output_namespace": "userform",
  "plugin_properties": {}
}
```

The plugin takes no configuration; `output_namespace` declares where submission payloads land — every `CompleteTaskStep` body for this subtask is written verbatim to `record.Data["userform"]`.

A more typical example:

```json
{
  "id": "applicant_confirmation_email",
  "task_type": "EMAIL",
  "plugin_properties": {
    "template_id": "phyto_confirmation_v2",
    "to_field": "userform.applicant_email"
  }
}
```

(No `output_namespace` here — this subtask runs synchronously inside the workflow and never receives a `CompleteTaskStep` call, so there's nothing to scope.)

### Why `output_namespace` exists

`CompleteTaskStep` is the external entry point for resuming a parked subtask with a payload. Without a scoping rule, callers could write *any* top-level key to `record.Data` — including keys owned by previous subtasks or internal coordinates like `_task_id`. That's a data-integrity hazard.

So writes are confined: the active subtask's `output_namespace` is the *only* top-level key the payload may land in. The frontend submits the inner form object; the orchestrator stamps the slot. A misconfigured template (missing `output_namespace`) results in the payload being dropped with a warning log — the workflow still resumes, so a config mistake doesn't break a running task.

The snapshot is stored on the `TaskRecord` (as `ActiveOutputNamespace`) at `StartSubTask` time, so subsequent `CompleteTaskStep` calls read it directly without re-resolving the registry.

The plugin (`EMAIL` in this example) unmarshals `plugin_properties` into its own typed config — see [`plugin-author-guide.md`](plugin-author-guide.md) for that side.

**Note** there is no `render_config_id` on a subtask template. Render configs are task-level — the same rendered view changes by `State`, and `State` is what plugins mutate.

---

## Workflow definition

Workflow definitions come from the [`go-temporal-workflow`](https://github.com/OpenNSW/go-temporal-workflow) engine — the orchestrator just consumes them. Refer to that project's docs for the authoritative schema; here is the shape relevant to this orchestrator.

A workflow is a graph of nodes connected by edges. Node types that matter for tasks:

- **`START`** / **`END`** — entry / exit
- **`TASK`** — in a *parent* workflow, this is what activates `StartTask`. In a *task* workflow, this is what activates `StartSubTask`. Carries a `task_template_id` referencing either a `TaskTemplate` or a `SubTaskTemplate`.
- **`GATEWAY`** — branching. Several gateway types exist; **parallel and inclusive splits are rejected** by `StartTask` (see [`architecture.md`](architecture.md) on the no-parallel-subtasks constraint).

**Demo files:**

- `demo/templates/graphs/workflow_phyto_journey.json` — parent workflow
- `demo/templates/graphs/*.json` — task workflows

If you're authoring these by hand, you'll typically:

1. Load the JSON in your registry: `registry.RegisterWorkflow(def)`.
2. Reference the workflow's `id` from a `TaskTemplate.workflow_id`.

---

## Render config

Render configs are **raw JSON** from the orchestrator's perspective — it never parses them. They live in `TaskRecord.RenderConfig` as `json.RawMessage` and are passed straight to your `Renderer.Render(ctx, config, facts)` implementation.

This means the schema is **whatever your renderer expects**. The demo's `SimpleRenderer` uses a state-keyed shape:

```json
{
  "id": "phyto_render_config",
  "STARTING": {
    "primary": { "type": "markdown", "payload": "Starting your phyto application…" }
  },
  "PENDING_USER": {
    "primary": { "type": "markdown", "payload": "Please complete the applicant form." }
  },
  "QUEUED_EXTERNALLY": {
    "primary": { "type": "markdown", "payload": "Your application is queued with the reviewing agency." }
  },
  "COMPLETED": {
    "primary": { "type": "markdown", "payload": "Your phyto application is complete." }
  },
  "default": {
    "primary": { "type": "markdown", "payload": "(no view configured for this state)" }
  }
}
```

`SimpleRenderer.Render(ctx, config, facts)` looks up `config[facts.State]`, falling back to `config["default"]`. Each entry is a `RenderResult` — a `slot → UIComponent` map.

`UIComponent`:

```go
type UIComponent struct {
    Type    string          `json:"type"`    // "markdown", "jsonforms", etc.
    Payload json.RawMessage `json:"payload"`
}
```

The `Type` strings (`"markdown"`, `"jsonforms"`, …) are conventions between your renderer and your frontend — not enforced by the library.

### Richer renderers

`SimpleRenderer` is intentionally minimal. Real consumers typically want:

- **Templated payloads.** Substitute `Record.Data` values into the payload (e.g. `"Hello {{userform.applicant_name}}"`).
- **Conditional slots.** A `sidebar` slot only when the user is a reviewer.
- **Schema lookups.** A `jsonforms` payload may reference a separate JSON Schema file, fetched by the renderer at render time.

The interface (`Renderer.Render(context.Context, json.RawMessage, Facts) (json.RawMessage, error)`) is deliberately open — write whatever fits your UI.

### Snapshot semantics

When `StartTask` runs, the render config's bytes are copied into `TaskRecord.RenderConfig`. **Subsequent edits to the registry don't affect existing tasks.** This is intentional:

- Audit: replaying a task months later renders the same view it did originally.
- Migration safety: changing a render config doesn't break tasks already in flight.

If you need to push a new config to existing tasks, you'd have to update `TaskRecord.RenderConfig` directly via `SaveTask` — there's no built-in API for that, and it's a deliberate friction.

---

## How they connect

```
[Parent Workflow node]
        │
        │ task_template_id: "demo_phyto_application_task"
        ▼
[TaskTemplate]
        ├── workflow_id: "Phyto_Application_Flow_v1"      ─► [WorkflowDefinition]   (child workflow runs)
        └── render_config_id: "phyto_render_config"       ─► [Render config JSON]   (snapshotted into TaskRecord)


[Task Workflow node]
        │
        │ task_template_id: "demo_generic_user_input"
        ▼
[SubTaskTemplate]
        ├── task_type: "USER_INPUT"                       ─► [Plugin registered as "USER_INPUT"]
        ├── plugin_properties: {...}                      ─► passed verbatim to plugin.Execute
        └── output_namespace: "userform"                  ─► CompleteTaskStep payload → record.Data["userform"]
```

Two distinct "TaskTemplateID" namespaces — one for tasks, one for subtasks — both resolved through the same registry interface but via different methods (`GetTaskTemplate` vs `GetSubTaskTemplate`). Don't reuse an ID across the two; even though the registry stores them in separate maps, it'll trip up anyone reading the templates.

---

## Loading at startup

The demo loads from disk:

```go
registry := NewTemplateRegistry()
if err := loadTemplates(registry, "demo/templates"); err != nil {
    log.Fatalln("Failed to load task template registry:", err)
}
```

`loadTemplates` walks the directory, identifies each file by shape, and calls the appropriate `Register*` method on `TemplateRegistry`. For your service:

- **Static JSON** — copy the demo's loader.
- **Database-backed** — implement `TaskTemplateRegistry` to read from your tables on demand, or load on boot.
- **Hot reload** — keep the registry thread-safe; swap entries at runtime. Existing tasks are unaffected (render configs are snapshotted), but newly-started tasks pick up the new versions.

---

## Validation tips

The orchestrator does minimal validation — bad templates surface as runtime errors deep inside `StartTask`/`StartSubTask`. For a smoother authoring experience:

- **At load time**, verify every `TaskTemplate.WorkflowID` resolves, and every `RenderConfigID` resolves.
- **At load time**, verify every `SubTaskTemplate.TaskType` has a registered plugin.
- **At test time**, run a smoke test that drives a happy-path workflow end-to-end (`demo/renderer_test.go` is an example).

---

## See also

- [`integration-guide.md`](integration-guide.md) — how the registry plugs into the orchestrator
- [`plugin-author-guide.md`](plugin-author-guide.md) — the `plugin_properties` consumer side
- [`frontend-guide.md`](frontend-guide.md) — what the rendered output ends up looking like