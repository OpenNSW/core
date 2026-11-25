# payment

Connects `core/payment` to the task engine and to `refid`: it issues the references payers pay against, and settles
a payment step when its payment completes.

## Payment references

`RefGenerator` implements `core/payment`'s `ReferenceGenerator` from a `refid.Registry`.

```go
svc := payment.NewService(repo, gateways, platformpayment.NewRefGenerator(refIDs, "ACME"))
```

A checkout uses the fee's own reference format when its metadata names one, and the default format otherwise: the
issuer `NewRefGenerator` is given, with `DefaultReferenceIDType`. `ReferenceMetadata` builds that metadata:

```go
metadata := platformpayment.ReferenceMetadata("ACME", "fee_ref", map[string]string{"office": "CMB"})
```

The default format has to be defined in the deployment's `refid` configuration.

## Settling a payment step

`TaskCompleter` wraps the task manager's completer. `core/payment` completes a step with the payment's outcome and no
command. A step's authorization rule is keyed by command, so the wrapper names `StepCommand` on a completion that
carries none.

```go
svc.SetTaskCompleter(platformpayment.NewTaskCompleter(systemTasks))
```
