# temporal

Thin factory for creating a configured [Temporal](https://temporal.io/) client. Used by both the macro workflow runner and the taskflow micro-workflow subsystem.

## Usage

```go
import "github.com/OpenNSW/core/temporal"

client, err := temporal.NewClient(temporal.Config{
    Host:      "localhost",
    Port:      7233,
    Namespace: "default",
})
if err != nil {
    log.Fatal(err)
}
defer client.Close()
```

The returned `client.Client` is the standard Temporal Go SDK client. Pass it to `workflow.NewTemporalManager` and `taskflow/orchestrator.NewTaskManager`.

## Config

| Field | Type | Description |
|---|---|---|
| `Host` | `string` | Temporal server hostname |
| `Port` | `int` | Temporal server port (default `7233`) |
| `Namespace` | `string` | Temporal namespace (default `"default"`) |

## Local development

Run a local Temporal server with the [Temporal CLI](https://docs.temporal.io/cli/):

```sh
temporal server start-dev
```

This starts a server at `localhost:7233` in the `default` namespace with an in-memory store and an embedded UI at `http://localhost:8233`.
