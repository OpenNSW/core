# taskauthz

The shared vocabulary of task authorization: who the caller is, what the deployment's logical principal names mean,
and which roles the caller may act in on a task. It holds no policy. The packages that decide whether a request may
read or change a task build on these types, so they agree on one identity model and one eligibility rule.

## Identity on the context

An HTTP middleware resolves the caller once and attaches an `Input`. Evaluators further down read it back.

```go
ctx = taskauthz.WithInput(ctx, taskauthz.Input{
    Kind:       taskauthz.KindUser,
    Roles:      principal.Roles,  // token roles
    UserID:     userID,           // internal user ID, the holder of task claims
    OwnedRoles: ownedRoles,       // lazy: which roles the caller's company holds on a case
})

in, ok := taskauthz.InputFromContext(ctx) // ok is false for an unauthenticated request
```

| `PrincipalKind` | Who |
|---|---|
| `KindUser` | A portal user |
| `KindClient` | A machine (M2M) client, named by `ClientID` |
| `KindSystem` | This service acting on its own authority, such as a payment or webhook handler completing a step |

`WithSystem(ctx)` attaches the system principal. No request can reach it: only code that is itself the authority for
a step uses it. Rules name it as `SystemPrincipal` (`"system"`), which the catalog may not define as a role or client.

## Eligibility

```go
catalog := taskauthz.Catalog{
    Roles:   map[string]string{"trader": "Trader", "cha": "CHA"}, // logical name -> token role
    Clients: map[string]string{"fcau": "FCAU_TO_NSW"},            // logical name -> client ID
}

e, err := catalog.Eligible(ctx, in, caseID, []string{"trader", "cha"})
if e.Allows("cha") { /* the caller may act as the case's CHA */ }
```

A caller may act in a role only when they hold its token role **and** their company holds that role on the task's
case. Holding the token role alone isn't enough.

- `Eligible` resolves ownership at most once, and only when the caller holds at least one of the token roles asked
  about, because `OwnedRoles` costs a database round trip.
- A client principal, or a user with no `OwnedRoles`, is eligible for nothing. That's reported as a result, not an
  error, so the policy layer decides what the denial means.
- `HoldsAny` tells "not this kind of user" apart from "this kind of user, but not their case", for logging.
- `RoleNames` lists every logical role in a stable order, for callers that consider all of them.

`Catalog` is the slice of the deployment's catalog (`platform/catalog`) that task authorization needs. The
composition root builds it and passes it in, so this package reads no configuration.
