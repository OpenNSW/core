# authn

JWT-based authentication. Validates tokens against a JWKS endpoint, extracts identity claims, and provides HTTP middleware that injects an `AuthContext` into each request's context.

## Configuration

```go
import "github.com/OpenNSW/core/authn"

manager, err := authn.NewManager(userProfileSvc, authn.Config{
    JWKSURL:   "https://idp.example.com/.well-known/jwks.json",
    Issuer:    "https://idp.example.com",
    Audience:  "my-api",
    ClientIDs: []string{"my-m2m-client"},
})
```

`UserProfileService` is optional. When provided, it is called on the first appearance of a user token to create or retrieve a persisted user record (e.g. to assign an internal user ID). Pass `nil` to skip user persistence.

## Middleware

```go
// 401 if no valid token
mux.Handle("/api/v1/tasks", manager.RequireAuthMiddleware()(handler))

// Proceeds with or without a token; handler checks presence itself
mux.Handle("/api/v1/public", manager.OptionalAuthMiddleware()(handler))
```

## Reading identity in handlers

```go
authCtx := authn.GetAuthContext(r.Context())
if authCtx == nil {
    // no token present (only possible with OptionalAuthMiddleware)
}

// Human user token
if authCtx.Type() == authn.UserPrincipalType {
    fmt.Println(authCtx.User.Email)
    fmt.Println(authCtx.User.ID)       // internal persisted ID (if UserProfileService set)
    fmt.Println(authCtx.User.OUID)     // organisation unit ID
}

// Machine-to-machine token
if authCtx.Type() == authn.ClientPrincipalType {
    fmt.Println(authCtx.Client.ClientID)
}
```

## Principal fields

**`UserContext`**

| Field | Description |
|---|---|
| `ID` | Internal persisted user ID (set by `UserProfileService`) |
| `IDPUserID` | Subject claim from the token |
| `Email` | Email claim |
| `PhoneNumber` | Phone number claim |
| `OUID` | Organisation unit ID |
| `OUHandle` | Organisation unit handle |
| `Roles` | Role claims |
| `Scopes` | Scope claims |

**`ClientContext`**

| Field | Description |
|---|---|
| `ClientID` | Client ID claim |
| `Roles` | Role claims |
| `Scopes` | Scope claims |

## Health check

```go
if err := manager.Health(ctx); err != nil {
    // JWKS endpoint unreachable
}
```

## Authorization

`authn` handles identity only. For scope enforcement use the [`authz`](../authz/README.md) package — `*AuthContext` satisfies `authz.Principal` directly.
