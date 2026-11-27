# tasktypes

The task types any single window can use: the plugins a workflow template names in a step's `task_type` that need
nothing specific to one deployment. They run on `taskflow`'s plugin registry, next to core's own `taskflow/plugins`.

## Registering them

Each task type has a `TaskType` constant and a constructor. The application registers the ones its workflows use,
so it builds only the dependencies those need:

```go
reg := plugins.NewRegistry()
err := reg.Register(tasktypes.TaskTypePayment, tasktypes.NewPaymentPlugin(paymentService))
```

| Task type | Constructor | Needs | What a step does |
|---|---|---|---|
| `USER_INPUT` | core's `plugins.NewUserInputPlugin` | Nothing | Waits for a form submission |
| `VALUE_WRITER` | `NewValueWriterPlugin` | Nothing | Writes values into a copy of the step's inputs, such as one number into every item of a list |
| `EXTERNAL_REVIEW` | `NewExternalReviewPlugin` | A `remote.Manager`, `FileTokens` and `CaseAttributes` | Starts a review flow on another service, sending the files in it as tokens for that service, and parks until it calls back |
| `PAYMENT` | `NewPaymentPlugin` | A `core/payment` service | Opens a checkout session and parks in `PENDING_PAYMENT` |
| `API_CALL` | core's `plugins.NewAPICallPlugin` | A dispatcher | Posts the step's inputs to a URL, without authentication |
| `AUTH_API_CALL` | `NewAPICallPlugin` | A `remote.Manager` | Calls a service from the `remote` manager's list and records the outcome, retrying a failure retrying can fix |
| `REFID_GENERATOR` | `NewRefIDGeneratorPlugin` | A `refid.Registry` | Generates reference IDs |
| `HTML_DOCUMENT_GENERATOR` | `NewHTMLDocumentGeneratorPlugin` | An `HTMLTemplateSource`, a `DocumentSaver` and `StaticData` | Renders and stores an HTML document |
| `REMOTE_FILE_ARCHIVER` | `NewRemoteFileArchiverPlugin` | A `RemoteFetcher` and a `DocumentSaver` | Stores a copy of a document another service links to |
| `NOTIFICATION` | `NewNotificationPlugin` | A sender such as `*notification.Manager` | Sends an email or SMS |

A constructor panics on a missing dependency, and `NewExternalReviewPlugin` on `FileTokens` that set only some
of their functions.

## Calling a service with an interpreter

`APICallPlugin` leaves the request body and the reading of the response to an `Interpreter`, so one plugin serves
any integration. `NewAPICallPluginWithInterpreter`
takes an integration's own interpreter, which can also implement `MultipartInterpreter`, `HeaderInterpreter` or
`QueryInterpreter` to shape the request, and `DispatchRecorder` to record what was sent.

An `Interpreter` reports every outcome, failures included, through `Interpret`, and the step completes. An
interpreter that also implements `RawInterpreter` reads the whole response (status, headers and body) through
`InterpretRaw` and decides what happens to the step: an error fails it, so the engine retries it, and a
`plugins.Permanent` error parks it. For such an interpreter, a `service_id` the manager doesn't list parks the step
as a configuration error.

`NewAPICallPlugin` uses the passthrough interpreter. It sends the step's `payload` input as the JSON body, or all its
mapped inputs when it maps no `payload`, and reads the response by its status (`statusOutcome`, which any
`RawInterpreter` can use):

| Response | Step | Output |
|---|---|---|
| 2xx | Completes | `{status, response, error: ""}` |
| 409 | Completes: what the call addresses is no longer waiting for it, typically because an earlier attempt got through | `{status, response, error: ""}` |
| 401, 403 | Parks for an admin | None |
| 408, 429, 5xx, transport error | Fails, so the engine retries it | None |
| Any other status | Completes, for the workflow to route | `{status, response, error}` |

`response` is the decoded JSON body, or its text when it isn't JSON, and is left out when the body is empty.
`error` is the body's `error` or `message` field, otherwise the status, such as `404 Not Found`. Workflows check
`error`, not `status`. Each refusal from a `RawInterpreter` is logged as `api_call: call refused`, with the task,
the service and the status.

```go
reg.Register("ACME_ORDER_SUBMIT", tasktypes.NewAPICallPluginWithInterpreter(remoteManager, acme.NewOrderInterpreter()))
```

Each step's `plugin_properties` names a `service_id` and a `path`. The service's base URL, authentication and
timeout come from the `remote` manager's configuration, not the template.
