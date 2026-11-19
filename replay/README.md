# replay

A small, data-driven engine for end-to-end tests. It runs an ordered list of steps from a JSON flow file against a
running backend, so a new scenario is a new flow file rather than new test code. It speaks only HTTP and JSON, and
uses only the standard library.

The engine targets the API shape of a consignment-based single window: `wait` polls `GET
/api/v1/consignments/{consignmentId}`, and `upload` reserves files with `POST /api/v1/storage`.

## Running a flow

```go
flow, err := replay.LoadFlow("flows/fcau_application_approve.json")

r := replay.New(server.URL, client) // a nil client means http.DefaultClient
r.Logf = t.Logf
r.Agency = mockAgency           // needed by callback steps
r.PaymentGateway = mockGateway  // needed by pay steps

if err := r.Run(ctx, flow); err != nil {
    t.Fatal(err)
}
```

`Run` executes the steps in order and stops at the first error. Variables live in `r.Vars`.

## Flow files

```json
{
  "name": "consignment_flows",
  "steps": [
    {
      "name": "trader creates consignment",
      "request": {
        "actor": "trader",
        "method": "POST",
        "path": "/api/v1/consignments",
        "expectStatus": 201,
        "extract": { "consignmentId": "id" }
      }
    },
    {
      "name": "wait for Initialize Consignment task",
      "wait": { "node": "Initialize Consignment", "state": "IN_PROGRESS", "into": "initTask" }
    }
  ]
}
```

Each step has a `name` and exactly one of:

| Step | Does |
|---|---|
| `request` | Sends `method` to `path` as `actor` with an optional `body`, checks `expectStatus` (default 200), and copies response fields into variables with `extract` (dot paths; a number indexes an array, as in `flows.0.workflowId`). For a GET, `retry` (such as `"30s"`) repeats it until the status and every extracted field are there. |
| `wait` | Polls the consignment in `consignmentId` until a workflow node whose name contains `node` reaches `state`, and can store its task ID in `into`. Default timeout 45s. |
| `callback` | Has `Agency` complete the parked external-review task in `taskVar` with `command` and `content`. Default timeout 30s. |
| `pay` | Has `PaymentGateway` confirm the payment created for the task in `taskVar`, through gateway `method` with `status` (default `paid`). Default timeout 45s. |
| `upload` | Uploads `content` as `filename` with `mimeType`, as `actor`, the way a portal does, and stores the file's reference in `into` for a form's file field. |

Paths and request bodies can use variables as `{{name}}`. `wait`, `callback` and `pay` take a `timeout` such as
`"60s"`.

## Actors

Each step names an `actor`. The engine sends it in the `X-Auth-Actor` header (`AuthActorHeader`), and the test's
transport or authentication stub maps it to a real identity. The engine itself knows nothing about tokens.

## Mocks

`callback` and `pay` steps drive test doubles the caller provides:

```go
type Agency interface {
    Respond(ctx context.Context, taskID, command string, content map[string]any, timeout time.Duration) error
}

type PaymentGateway interface {
    Pay(ctx context.Context, taskID, method, status string, timeout time.Duration) error
}
```
