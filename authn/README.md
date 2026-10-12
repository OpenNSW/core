# authn

JWT-based authentication. Validates tokens against a JWKS endpoint, extracts identity claims, and provides HTTP middleware that injects an `AuthContext` into each request's context.

## Configuration

```go
import "github.com/OpenNSW/core/authn"

manager, err := authn.NewManager(authn.Config{
    JWKSURL:  "https://idp.example.com/.well-known/jwks.json",
    Issuer:   "https://idp.example.com",
    Audience: "my-api",
})
```

`Config` carries `yaml` struct tags, so it can be embedded in a larger application config struct and populated generically (e.g. via `yaml.Unmarshal`):

```yaml
authn:
  jwksURL: https://idp.example.com/.well-known/jwks.json
  issuer: https://idp.example.com
  audience: my-api
```

A token is accepted when its signature, `iss`, `aud` and `exp` check out and it carries non-empty `sub` and `client_id` claims. How the token was obtained (`grant_type`) plays no part: a person signing in through a client and a system using client credentials get the same `Principal`, distinguished only by the roles, scopes and claims they carry. Every other claim in the token is passed through as `ExtraClaims` — nothing has to be declared.

> **Note:** `audience` is what binds a token to this service: any client the IdP issues a token for this audience is accepted. Which clients may call the service is authorization policy, not this package's concern — restrict it in authz or the application using `AuthContext.ClientID()`. If your IdP sets `aud` to the requesting client rather than the API, configure an audience / API resource for the service in the IdP.

## Middleware

```go
// 401 if no valid token
mux.Handle("/api/v1/tasks", manager.RequireAuthMiddleware()(handler))

// Proceeds with or without a token; handler checks presence itself
mux.Handle("/api/v1/public", manager.OptionalAuthMiddleware()(handler))
```

`authn` only authenticates. Anything your application does with the validated principal — provisioning a user record, loading a profile, attaching tenant data — goes in your own middleware, chained after `authn`'s:

```go
// in the application, not in authn
func ProvisionUser(users *user.Service) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            authCtx := authn.GetAuthContext(r.Context())
            u, err := users.GetOrCreate(r.Context(), authCtx.Subject(), authCtx.ExtraClaims())
            if err != nil {
                http.Error(w, "user provisioning failed", http.StatusInternalServerError)
                return
            }
            next.ServeHTTP(w, r.WithContext(user.NewContext(r.Context(), u)))
        })
    }
}

// wrap all protected routes once, not per route
api := http.NewServeMux()
api.HandleFunc("POST /api/x", handlerX)

root := http.NewServeMux()
root.Handle("/api/", manager.RequireAuthMiddleware()(ProvisionUser(users)(api)))
```

The application owns that policy: who gets a record, what goes on the context, and whether a failure rejects the request.

## Reading identity in handlers

```go
authCtx := authn.GetAuthContext(r.Context())
if authCtx == nil {
    // no token present (only possible with OptionalAuthMiddleware)
    return
}

fmt.Println(authCtx.Subject())                        // JWT "sub"
fmt.Println(authCtx.ClientID())                       // JWT "client_id"
fmt.Println(authCtx.Roles())                          // role claims
fmt.Println(authCtx.Scopes())                         // scope claims
fmt.Println(authCtx.ExtraClaims().String("email"))    // any other claim — see "Extra claims"
```

The accessors are nil-safe, so they can be called on a nil `*AuthContext`:

```go
email := authn.GetAuthContext(r.Context()).ExtraClaims().String("email")
```

The same values are available as fields on `authCtx.Principal`.

## Extra claims (beyond the fixed schema)

`authn`'s fixed schema is deliberately small: `sub`, `client_id`, `scope`, and `roles` (plus the JWT registered claims `iss`, `aud`, `exp`, `nbf`, `iat`, `jti`). Every other claim in the token — `email`, `phone_number`, `ouId`, `grant_type`, an IdP-specific claim like `given_name`, a namespaced claim like `https://app.example.com/department` — is passed through in `ExtraClaims`, keyed by its exact name.

```go
authCtx := authn.GetAuthContext(r.Context())
email  := authCtx.ExtraClaims().String("email")     // "" if absent or not a string
groups := authCtx.ExtraClaims().Strings("groups")   // JSON array of strings, or a space-delimited string
dept   := authCtx.ExtraClaims()["department"]       // any other shape: index the map directly
```

`authn` does not decide which of these a request needs. If your application depends on a claim — an organization/tenant identifier used for scoping, say — check it in your own middleware or handler, so a token without it is rejected rather than silently scoped to `""`:

```go
if authn.GetAuthContext(r.Context()).ExtraClaims().String("ouId") == "" {
    http.Error(w, "forbidden", http.StatusForbidden)
    return
}
```

**Semantics:**
- Claims `authn` binds itself never appear in `ExtraClaims`, so each value has one home: read `Subject()`, not `ExtraClaims()["sub"]`. Case variants of fixed-schema names (`Roles`, `SUB`) are excluded too, because `encoding/json` matches struct tags case-insensitively and binds them to the fixed field. A remapped roles claim (see below) is excluded under its exact name.
- A claim whose value is JSON null or an empty/whitespace-only string is dropped, so it reads the same as a claim the token never carried. A token with no other claims has a nil `ExtraClaims`.
- Claim names are matched **exactly** — JWT claim names are case-sensitive (RFC 7519 §4). Nested lookups are not supported: a name is a top-level key, which is what lets namespaced names like `https://app.example.com/roles` work verbatim.
- Values are the claim's JSON-decoded Go representation, with one normalization: every JSON string a value *directly* carries is whitespace-trimmed — the value itself, and the elements of a top-level array of strings. Nested objects are never rewritten. (Fixed-schema claims such as `sub` are decoded separately and are not trimmed.)
- Use `ExtraClaims.String(name)` for a plain string value and `ExtraClaims.Strings(name)` for a list-shaped claim. `Strings` accepts a JSON array of strings *or* a single space-delimited string (the convention the `scope` claim uses), so it whitespace-splits free text — use `String` for anything that is not list-shaped. Both are nil-safe and return the zero value for absent/wrong-shape claims; they never panic or error.

## Remapping the roles claim

Roles populate `Principal.Roles` and `AuthContext.Roles()`, which is what the [`authz`](../authz/README.md) seam consumes. If your IdP does not emit a top-level `roles` claim, point `authn` at the one it does emit:

```go
authn.Config{ /* ... */ RolesClaim: "groups" }   // or authn.WithRolesClaim("groups")
```

The claim must be a top-level JSON array of strings — exactly the shape the default `roles` claim must have. An absent claim yields no roles; a present claim of any *other* shape rejects the token, so a mistyped name fails loudly on the first request rather than silently disabling every role check.

Dotted paths into nested objects (Keycloak's `realm_access.roles`) are **not** supported — flatten them with an IdP-side protocol mapper. Exact matching is what keeps namespaced names such as `https://app.example.com/roles` working.

## Principal fields

| Field | Description |
|---|---|
| `Subject` | JWT `sub` claim: the IdP's ID for the caller |
| `ClientID` | JWT `client_id` claim: the OAuth2 client the token was issued to |
| `Roles` | Role claims |
| `Scopes` | Scope claims |
| `ExtraClaims` | Every other claim in the token — see "Extra claims" above |

## Migrating from user and client principals

Earlier versions split every caller into a user or a client principal by the token's `grant_type` claim. There is now one `Principal`.

| Before | After |
|---|---|
| `NewManager(userProfileSvc, cfg)` | `NewManager(cfg)` |
| `Middleware(userProfileSvc, extractor)` / `RequireAuth(userProfileSvc, extractor)` | `Middleware(extractor)` / `RequireAuth(extractor)` |
| `UserProfileService.GetOrCreateUser` | Your own middleware chained after `authn` — see "Middleware" |
| `authCtx.User.ID` (persisted ID) | Whatever your provisioning middleware puts on the context |
| `authCtx.User.IDPUserID` | `authCtx.Subject()` |
| `authCtx.Client.ClientID` | `authCtx.ClientID()` |
| `authCtx.User.Roles` / `authCtx.Client.Roles` (and `Scopes`, `ExtraClaims`) | `authCtx.Roles()` (and `Scopes()`, `ExtraClaims()`) |
| `authCtx.Type()`, `UserPrincipalType`, `ClientPrincipalType` | Removed — branch on roles, scopes or claims instead |
| `UserClaims` / `ClientClaims` (`ClaimSpec{Optional, Required}`), `WithUserClaims` / `WithClientClaims` | Removed — every claim is in `ExtraClaims`; check required ones in your own middleware |

Behaviour changes the compiler will not catch:

- **`Subject()` now returns the JWT `sub` claim.** For user tokens it used to return the persisted user ID from `UserProfileService` (falling back to `sub`), and for client tokens it returned the `client_id`. Anything keyed by `Subject()` — stored records, audit entries, cache keys — must be checked.
- **`sub` is required on every token.** Client-credentials tokens without `sub` used to be accepted; they are now rejected.
- **`grant_type` is ignored.** Tokens without it, or with any value, are accepted. It now passes through in `ExtraClaims` like any other claim.
- **Required claims are no longer enforced by `authn`.** A token missing a claim you previously listed as `Required` is now accepted; move that check into your own middleware.
- **`ExtraClaims` now holds every non-fixed claim**, not just declared ones. Code that treated "present in `ExtraClaims`" as "declared in config" must be checked.
- **The JSON shape of `AuthContext` changed**, for anything that serializes it.

## Health check

```go
if err := manager.Health(); err != nil {
    // auth components are not initialized
    // (this does NOT check JWKS reachability)
}
```

## Authorization

`authn` handles identity only. For scope enforcement use the [`authz`](../authz/README.md) package — `*AuthContext` satisfies `authz.Principal` directly.
