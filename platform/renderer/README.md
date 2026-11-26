# renderer

UI projectors for `core/uiprojector` that the platform's task types need.

| Projector | Type | What it renders |
|---|---|---|
| `PaymentProjector` | `PAYMENT` | The payment instructions of a step in `PENDING_PAYMENT` |

```go
projectors := append(uiprojector.DefaultProjectors(), renderer.NewPaymentProjector())
assembler, err := uiprojector.NewAssembler(templates, projectors)
```

`PaymentProjector` renders the payment facts (reference, amount, instructions, flow type) that the `PAYMENT` task type
writes on the task record when it opens the checkout session. It does not call the payment service. A section with its
own markdown template (`templateContent`) is rendered with the payment data, and one without it shows the gateway's
instructions as they are.
