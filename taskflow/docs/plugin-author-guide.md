# Plugin Author Guide

How to write a new subtask plugin and register it with the orchestrator.

> Prerequisite: read [`architecture.md`](architecture.md) — particularly the lifecycle and suspend/resume sequence diagram.

---

## What a plugin is

A plugin is the strategy that executes a single subtask node inside a task workflow. When the task workflow activates a SUBTASK node, the orchestrator:

1. Loads the `TaskRecord`.
2. Resolves the `SubTaskTemplate` from `payload.TaskTemplateID`.
3. Looks up a plugin by `subTemplate.TaskType`.
4. Calls `plugin.Execute(pluginCtx, subTemplate.PluginProperties)`.

Your plugin is in charge of:

- Doing the actual work (sending an email, calling an API, queuing for review).
- Mutating `ctx.Record.State` to reflect what's happening.
- Deciding whether the workflow should advance immediately (`return nil`) or park (`return ErrSuspended`).

The orchestrator handles persistence around you — you mutate the record in-place; it calls `SaveTask` after you return.

---

## The interface

```go
type TaskPlugin interface {
    Execute(ctx PluginContext, config json.RawMessage) error
}

type PluginContext struct {
    Context context.Context
    Record  *store.TaskRecord
    Inputs  map[string]any
}
```

**`config`** is the JSON blob from the subtask template's `plugin_properties`. Unmarshal it into a typed struct your plugin defines.

**`ctx.Record`** is a pointer — mutate it directly. The orchestrator persists whatever you leave behind.

**`ctx.Inputs`** is the (already namespaced) inputs map for this subtask invocation. Convenience for plugins that need raw inputs without reaching into `Record.Data`.

---

## The two return modes

| Return value | What it means | Workflow effect |
| --- | --- | --- |
| `nil` | Synchronous completion. The plugin did its work; advance immediately. | Task workflow proceeds to the next node. |
| `plugins.ErrSuspended` | Async — work is in flight, wait for an external resume. | Activity parks. `CompleteTaskStep` (or any other resume path) wakes it. |
| any other `error` | Failure. | Activity returns the error to Temporal; retry policy applies. |

A plugin that suspends typically sets `Record.State` to a recognisable value first (e.g. `PENDING_USER`, `QUEUED_EXTERNALLY`) so the UI knows what to render while waiting.

---

## Two worked examples

### Synchronous: `APICallPlugin` (fire-and-forget)

```go
type APICallConfig struct {
    URL string `json:"url"`
}

func (p *APICallPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
    var cfg APICallConfig
    if err := json.Unmarshal(configRaw, &cfg); err != nil {
        return fmt.Errorf("failed to parse generic_api_call config: %w", err)
    }
    if cfg.URL == "" {
        return fmt.Errorf("missing 'url' in generic_api_call config")
    }

    ctx.Record.State = "DISPATCHED"

    if err := p.dispatcher(ctx.Context, cfg.URL, ctx.Record.TaskID, ctx.Record.Data); err != nil {
        return fmt.Errorf("api call dispatch failed: %w", err)
    }
    return nil // sync completion — workflow advances
}
```

Pattern:

1. Parse typed config.
2. Validate.
3. Set state.
4. Do the work.
5. Return `nil` — no parking needed.

### Suspending: `UserInputPlugin`

```go
type UserInputConfig struct {
    StatusOverride string `json:"status_override,omitempty"`
}

func (p *UserInputPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
    status := "PENDING_USER"
    if len(configRaw) > 0 && string(configRaw) != "null" {
        var cfg UserInputConfig
        if err := json.Unmarshal(configRaw, &cfg); err == nil {
            if cfg.StatusOverride != "" {
                status = cfg.StatusOverride
            }
        }
    }
    ctx.Record.State = status
    return ErrSuspended // park; portal will POST /api/task/{taskID} later
}
```

Pattern:

1. Decide what state to display while waiting.
2. Mutate the record.
3. Return `ErrSuspended` — the activity parks; the parent workflow doesn't see anything happen until somebody calls `CompleteTaskStep`.

`UserInputPlugin` does nothing else — the actual data lands in `Record.Data` when `CompleteTaskStep` runs, not inside `Execute`.

---

## Writing your own: a recipe

```go
package myplugins

import (
    "encoding/json"
    "fmt"

    "github.com/OpenNSW/nsw-task-flow/plugins"
)

// 1. Define your typed config — the shape that subtask templates will use.
type EmailPluginConfig struct {
    TemplateID string `json:"template_id"`
    ToField    string `json:"to_field"` // dotted key into Record.Data, e.g. "applicant.email"
}

// 2. Define the plugin and its dependencies.
type EmailPlugin struct {
    sender EmailSender
}

func NewEmailPlugin(sender EmailSender) plugins.TaskPlugin {
    return &EmailPlugin{sender: sender}
}

// 3. Implement Execute.
func (p *EmailPlugin) Execute(ctx plugins.PluginContext, configRaw json.RawMessage) error {
    var cfg EmailPluginConfig
    if err := json.Unmarshal(configRaw, &cfg); err != nil {
        return fmt.Errorf("email plugin: invalid config: %w", err)
    }
    if cfg.TemplateID == "" || cfg.ToField == "" {
        return fmt.Errorf("email plugin: template_id and to_field are required")
    }

    to, ok := getNested(ctx.Record.Data, cfg.ToField)
    if !ok {
        return fmt.Errorf("email plugin: no value at %q in task data", cfg.ToField)
    }

    ctx.Record.State = "EMAIL_DISPATCHED"
    if err := p.sender.Send(ctx.Context, to.(string), cfg.TemplateID, ctx.Record.Data); err != nil {
        return fmt.Errorf("email send failed: %w", err)
    }
    return nil // sync — nothing to wait for
}
```

Register it:

```go
pluginsReg.Register("EMAIL", NewEmailPlugin(mySender))
```

Reference it from a subtask template:

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

---

## Doing it right

### State naming

Use stable, all-caps state strings (`PENDING_USER`, `EMAIL_DISPATCHED`, `WAITING_FOR_PAYMENT`). The renderer keys on these — typos and renames are silent UI regressions.

Three rough conventions used by the built-in plugins:

- `PENDING_*` for human-blocked steps (`PENDING_USER`, `PENDING_PAYMENT`)
- `QUEUED_*` for handed-off-to-external-system steps
- `*_DISPATCHED` / `*_SENT` for synchronously-completed steps

### Configuration

Always define a typed struct for your config. Don't reach into `configRaw` with `gjson` or hand-rolled lookups — it's harder to test and you lose `omitempty` semantics.

If a required field is missing, fail with a descriptive error including the plugin name. This surfaces in Temporal worker logs and is the first thing an integrator will see when a template is wrong.

### Reading from `Record.Data`

`Record.Data` is a `map[string]any` shaped by all the *previous* subtasks. Each submission-style subtask owns one top-level slot, named by its template's `output_namespace`. A user-input subtask with `output_namespace: "userform"` produces `Data["userform"] = { ...the form payload... }`; your subtask reads it back at the same path.

Note: callers do **not** wrap their submission payload — the orchestrator writes the raw POST body into the declared slot. So `Data["userform"]` is the form object itself, not `{ "userform": {...} }`.

Use dotted-key helpers if you have them (the orchestrator uses `setNestedKey` for writes); for reads, walk the map explicitly to keep failure modes clear.

### Mutating `Record.Data`

If your plugin produces output (e.g. an external review's verdict), write it into `Record.Data` so downstream subtasks and the final `onTaskCompleted` callback can see it:

```go
ctx.Record.Data["email"] = map[string]any{
    "status":      "delivered",
    "message_id":  messageID,
    "delivered_at": time.Now(),
}
```

Use a namespace key matching your plugin's domain. Don't mutate keys owned by other subtasks.

### Context

`ctx.Context` is the Temporal activity context. It carries cancellation. Pass it to outbound HTTP / DB calls so long-running operations cancel cleanly when the activity times out.

### Idempotency

Temporal retries failed activities. If your plugin dispatches an external action, make it safe to retry — use idempotency keys, check before sending, or accept that duplicates are possible. `Record.TaskID` is a natural correlation key (it's stable and unique).

### Failure modes

| Failure | What to do |
| --- | --- |
| Bad config (missing field, wrong shape) | Return error; will surface as a workflow failure on first activation |
| External system is down | Return error; Temporal retries per activity policy |
| External system rejected the request (4xx) | Return error with the body; usually not retryable, but Temporal will try anyway unless you configure non-retryable errors |
| Internal logic error (data your plugin produced isn't valid) | Return error with detail |
| Long-running async work | Return `ErrSuspended` and arrange for someone to call `CompleteTaskStep` later |

### Don't

- **Don't call `tm.SaveTask` directly.** Mutate the record; the orchestrator persists for you.
- **Don't return `ErrResultPending`** from a plugin. That's the orchestrator's translation layer to Temporal. Use `plugins.ErrSuspended`.
- **Don't block forever in `Execute`.** If you need to wait, suspend.
- **Don't mutate `ctx.Inputs`.** It's the snapshot the orchestrator handed you; mutating it has no effect on persisted state. Mutate `ctx.Record.Data` instead.
- **Don't change `TaskID`, parent coordinates, `TaskWorkflowID`, or `ActiveOutputNamespace`.** Those are written by `StartTask` / `StartSubTask` and consumed by the resume path. Plugins should treat them as read-only.
- **Don't write to `Record.Data` under a top-level key you don't own.** Each submission-style subtask owns one slot (its template's `output_namespace`). If your plugin produces internal output, namespace it under your task type's own slot, not under another subtask's.

---

## Testing

```go
func TestEmailPlugin_Sends(t *testing.T) {
    sender := &fakeSender{}
    p := NewEmailPlugin(sender)

    record := &store.TaskRecord{
        TaskID: "test-task",
        Data: map[string]any{
            "userform": map[string]any{"applicant_email": "alice@example.com"},
        },
    }
    err := p.Execute(plugins.PluginContext{
        Context: context.Background(),
        Record:  record,
    }, json.RawMessage(`{"template_id":"t1","to_field":"userform.applicant_email"}`))

    if err != nil {
        t.Fatalf("Execute: %v", err)
    }
    if record.State != "EMAIL_DISPATCHED" {
        t.Errorf("state: got %q", record.State)
    }
    if sender.lastTo != "alice@example.com" {
        t.Errorf("to: got %q", sender.lastTo)
    }
}
```

Tests don't need the rest of the orchestrator — `PluginContext` is a plain struct. Plug in a fake collaborator, hand-build a `TaskRecord`, call `Execute`, assert on the mutations.

---

## See also

- [`integration-guide.md`](integration-guide.md) — wiring and `pluginsReg.Register(...)`
- [`template-reference.md`](template-reference.md) — the `SubTaskTemplate` shape your `plugin_properties` lives inside
- `plugins/user_input.go`, `plugins/api_call.go`, `plugins/external_review.go`, `plugins/payment.go` — built-in plugins as worked examples
