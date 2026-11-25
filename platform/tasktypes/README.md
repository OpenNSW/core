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
| `AUTH_API_CALL` | `NewAPICallPlugin` | A `remote.Manager` | Calls a service from the `remote` manager's list and records the outcome |
| `REFID_GENERATOR` | `NewRefIDGeneratorPlugin` | A `refid.Registry` | Generates reference IDs |
| `HTML_DOCUMENT_GENERATOR` | `NewHTMLDocumentGeneratorPlugin` | An `HTMLTemplateSource`, a `DocumentSaver` and `StaticData` | Renders and stores an HTML document |
| `REMOTE_FILE_ARCHIVER` | `NewRemoteFileArchiverPlugin` | A `RemoteFetcher` and a `DocumentSaver` | Stores a copy of a document another service links to |
| `NOTIFICATION` | `NewNotificationPlugin` | A sender such as `*notification.Manager` | Sends an email or SMS |

A constructor panics on a missing dependency, and `NewExternalReviewPlugin` on `FileTokens` that set only some
of their functions.

## Calling a service with an interpreter

`APICallPlugin` leaves the request body and the reading of the response to an `Interpreter`, so one plugin serves
any integration. `NewAPICallPlugin` sends the step's `payload` input as the JSON body, or all its mapped inputs when
it maps no `payload`. `NewAPICallPluginWithInterpreter`
takes an integration's own interpreter, which can also implement `MultipartInterpreter`, `HeaderInterpreter` or
`QueryInterpreter` to shape the request, and `DispatchRecorder` to record what was sent.

```go
reg.Register("ACME_ORDER_SUBMIT", tasktypes.NewAPICallPluginWithInterpreter(remoteManager, acme.NewOrderInterpreter()))
```

Each step's `plugin_properties` names a `service_id` and a `path`. The service's base URL, authentication and
timeout come from the `remote` manager's configuration, not the template.
