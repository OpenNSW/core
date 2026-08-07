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

    // Optional: declare any claims beyond the fixed schema you need. See
    // "Extra claims" below.
    UserClaims: authn.ClaimSpec{Optional: []string{"email", "ouId", "ouHandle"}},
})
```

`UserProfileService` is optional. When provided, it is called on the first appearance of a user token to create or retrieve a persisted user record (e.g. to assign an internal user ID). Pass `nil` to skip user persistence.

> **Note:** every token must carry a `grant_type` claim — it decides user vs. client principal, and a token without it is rejected with `unsupported grant type`. This is not a standard access-token claim (RFC 9068 does not define it), so an IdP that omits it will not work with this package as-is.

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
    return
}

// Human user token
if authCtx.Type() == authn.UserPrincipalType {
    fmt.Println(authCtx.User.ID)                             // internal persisted ID (if UserProfileService set)
    fmt.Println(authCtx.User.Roles)                          // role claims
    fmt.Println(authCtx.User.ExtraClaims.String("email"))    // "" unless declared — see "Extra claims"
    fmt.Println(authCtx.User.ExtraClaims.String("ouHandle"))
}

// Machine-to-machine token
if authCtx.Type() == authn.ClientPrincipalType {
    fmt.Println(authCtx.Client.ClientID)
}
```

`AuthContext` also exposes a nil-safe accessor seam that works for either principal type without branching — `Type()`, `Subject()`, `Roles()`, `Scopes()`, and `ExtraClaims()`:

```go
// Safe even when authCtx is nil or the request was a client token.
email := authn.GetAuthContext(r.Context()).ExtraClaims().String("email")
```

## Extra claims (beyond the fixed schema)

`authn`'s fixed schema is deliberately small: `client_id`, `grant_type`, `scope`, and `roles` (plus the JWT registered claims). Anything else — `email`, `phone_number`, `ouId`, `ouHandle`, an IdP-specific claim like `given_name`, or anything your identity provider emits — is declared explicitly via `Config` fields (`Manager` path) or functional options (direct `TokenExtractor` construction). Separate declarations exist for user-principal (`authorization_code` grant) and client-principal (`client_credentials` grant) tokens, since some claims are only meaningful for one or the other.

```go
manager, err := authn.NewManager(userProfileSvc, authn.Config{
    JWKSURL:   "...",
    Issuer:    "...",
    Audience:  "...",
    ClientIDs: []string{"my-m2m-client"},

    UserClaims: authn.ClaimSpec{
        Optional: []string{"email", "ouId", "ouHandle", "given_name"},
        Required: []string{"phone_number"},
    },
    ClientClaims: authn.ClaimSpec{
        Optional: []string{"department"},
    },
})
```

Or, constructing a `TokenExtractor` directly:

```go
extractor, err := authn.NewTokenExtractor(jwksURL, issuer, audience, clientIDs,
    authn.WithUserClaims(authn.ClaimSpec{
        Optional: []string{"email", "ouId", "ouHandle"},
        Required: []string{"phone_number"},
    }),
)
```

Reading extra claims in a handler:

```go
authCtx := authn.GetAuthContext(r.Context())
if authCtx == nil || authCtx.Type() != authn.UserPrincipalType {
    return
}
email  := authCtx.User.ExtraClaims.String("email")     // "" if not declared/present
groups := authCtx.User.ExtraClaims.Strings("groups")   // JSON array of strings, or a space-delimited string
roles  := authCtx.User.Roles                           // roles are fixed schema, not an extra claim
```

If a `UserProfileService` implementation needs one of these values (e.g. to scope users by organization), declare it and read it from the principal passed into `GetOrCreateUser` — see "Principal fields" below.

**Semantics:**
- **User-scoped vs. client-scoped.** `UserClaims` applies only to user-principal tokens, `ClientClaims` only to client-principal tokens. A name declared on one side is never extracted from the other.
- **Optional:** a claim that is absent, JSON-null, or an empty/whitespace-only string is silently skipped — the token is never rejected.
- **Required:** the token is rejected unless the claim carries a usable value — a non-blank string, or a non-empty array of non-blank strings. A number, boolean, object, empty array or mixed array is *not* accepted, because it would read back as `""`/`nil` and silently defeat the requirement. A name listed in both `Optional` and `Required` (in any order, across any number of calls) is required.
- Claim names are matched **exactly** — JWT claim names are case-sensitive (RFC 7519 §4) and only whitespace-trimmed. Nested lookups are not supported: a name is a top-level key, which is what lets namespaced names like `https://app.example.com/roles` work verbatim.
- Values are the claim's JSON-decoded Go representation, with one normalization: every JSON string a value *directly* carries is whitespace-trimmed — the value itself, and the elements of a top-level array of strings. Nested objects are never rewritten. (Fixed-schema claims such as `sub` are decoded separately and are not trimmed.)
- Use `ExtraClaims.String(name)` for a plain string value and `ExtraClaims.Strings(name)` for a list-shaped claim. `Strings` accepts a JSON array of strings *or* a single space-delimited string (the convention the `scope` claim uses), so it whitespace-splits free text — use `String` for anything that is not list-shaped. Both are nil-safe and return the zero value for absent/wrong-shape claims; they never panic or error.
- A claim already bound by `authn` — the fixed schema, any case variant of it, or your configured `RolesClaim` — cannot be declared as an extra claim. Construction (or `Config.Validate()`) fails fast. Case variants are rejected because `encoding/json` matches struct tags case-insensitively, so a payload key of `Roles` still lands in the fixed-schema roles field.
- A blank claim name is a construction error, not a silent no-op — a trailing comma in `MY_CLAIMS=email,` would otherwise drop a rule you asked for.

## Remapping the roles claim

Roles populate `Principal.Roles` and `AuthContext.Roles()`, which is what the [`authz`](../authz/README.md) seam consumes. If your IdP does not emit a top-level `roles` claim, point `authn` at the one it does emit:

```go
authn.Config{ /* ... */ RolesClaim: "groups" }   // or authn.WithRolesClaim("groups")
```

The claim must be a top-level JSON array of strings — exactly the shape the default `roles` claim must have. An absent claim yields no roles; a present claim of any *other* shape rejects the token, so a mistyped name fails loudly on the first request rather than silently disabling every role check.

Dotted paths into nested objects (Keycloak's `realm_access.roles`) are **not** supported — flatten them with an IdP-side protocol mapper. Exact matching is what keeps namespaced names such as `https://app.example.com/roles` working.

## Principal fields

**`UserContext`**

| Field | Description |
|---|---|
| `ID` | Internal persisted user ID (set by `UserProfileService`) |
| `IDPUserID` | Subject claim from the token |
| `Roles` | Role claims |
| `Scopes` | Scope claims |
| `ExtraClaims` | Claims beyond the fixed schema, populated only for names you declared — see "Extra claims" above |

**`ClientContext`**

| Field | Description |
|---|---|
| `ClientID` | Client ID claim |
| `Roles` | Role claims |
| `Scopes` | Scope claims |
| `ExtraClaims` | Claims beyond the fixed schema, populated only for names you declared — see "Extra claims" above |

**`UserProfileService`**

```go
type UserProfileService interface {
    GetOrCreateUser(ctx context.Context, principal *UserPrincipal) (string, error)
}
```

Called on first login for user-principal tokens. `principal.Subject` is the JWT `sub` claim — the IdP's user ID, *not* the persisted ID you return. `principal.ExtraClaims` holds whatever claims you declared; index it with `principal.ExtraClaims.String("email")`, etc.

`principal` is never nil and **must not be mutated**: the middleware has already built the request's `AuthContext` from it and shares the same `ExtraClaims` map.

## Migrating from the fixed email/phone/ouId/ouHandle fields

Those four claims used to be fixed-schema fields, populated on every user token. They are IdP-specific rather than standard OIDC, so they are now ordinary extra claims that you declare.

| Before | After |
|---|---|
| `authCtx.User.Email` | `authCtx.User.ExtraClaims.String("email")` + declare `"email"` |
| `authCtx.User.PhoneNumber` (`*string`) | `...String("phone_number")` + declare it |
| `authCtx.User.OUID` | `...String("ouId")` + declare it |
| `authCtx.User.OUHandle` | `...String("ouHandle")` + declare it |
| `UserPrincipal.UserID` | `UserPrincipal.Subject` |
| `GetOrCreateUser(ctx, idpUserID, email, phone, ouID, ouHandle)` | `GetOrCreateUser(ctx, principal)` |

**Declaring is not optional.** An undeclared claim reads back as `""` even when the signed token carries it, and nothing warns you — the compiler catches the removed struct fields and the changed `GetOrCreateUser` signature, but it cannot catch a missing declaration. If a value is load-bearing (an organization/tenant identifier used for scoping, say), put it in `Required` so a token without it is rejected rather than silently scoped to `""`:

```go
UserClaims: authn.ClaimSpec{
    Required: []string{"ouId"},
    Optional: []string{"email", "phone_number", "ouHandle"},
},
```

Earlier versions rejected a user token missing `email`, `ouId`, or `ouHandle`. That check is now yours to declare.

## Health check

```go
if err := manager.Health(); err != nil {
    // auth components are not initialized
    // (this does NOT check JWKS reachability)
}
```

## Authorization

`authn` handles identity only. For scope enforcement use the [`authz`](../authz/README.md) package — `*AuthContext` satisfies `authz.Principal` directly.
