# authz — generic, scope-based authorization

A small, **generic** authorization layer for HTTP services. It authorizes from the
**OAuth2 scopes carried on the token** and is fully decoupled from the
authentication layer.

## Design principles

- **Imports nothing internal.** The package depends only on the standard library.
  It defines a minimal `Principal` interface — `Subject()`, `Roles()`, `Scopes()` —
  which the authn layer's `*auth.AuthContext` satisfies **structurally**. authz
  never imports `internal/auth`.
- **Principal via dependency injection.** An `Extractor func(context.Context) (Principal, bool)`
  is supplied at construction. The composition root wires the authn lookup into it,
  so this package has no knowledge of how authentication works.
- **Scopes come from the token.** Decisions use the scopes the IdP granted on the
  token (e.g. `nsw:consignment:read`); there is no role- or client-ID-to-scope
  mapping. `Roles()` is exposed for finer, business-level checks at the service layer.
- **App scopes stay in the app.** Concrete scope strings (`nsw:...`) are defined at
  the composition root, never in this generic package.

## API

```go
type Principal interface { Subject() string; Roles() []string; Scopes() []string }
type Extractor func(context.Context) (Principal, bool)

func New(extract Extractor) (*Authorizer, error)

func (a *Authorizer) Principal(ctx context.Context) (Principal, bool)
func (a *Authorizer) RequireScope(scope string) func(http.Handler) http.Handler
func (a *Authorizer) RequireAnyScope(scopes ...string) func(http.Handler) http.Handler
func (a *Authorizer) RequireAllScopes(scopes ...string) func(http.Handler) http.Handler

func HasScope(p Principal, scope string) bool
func HasAnyScope(p Principal, scopes ...string) bool
func HasAllScopes(p Principal, scopes ...string) bool

var ErrUnauthenticated, ErrForbidden error
```

Middleware returns **401** when the request is unauthenticated and **403** when the
principal lacks the required scope(s).

## Wiring (composition root)

```go
authzr, err := authz.New(func(ctx context.Context) (authz.Principal, bool) {
    ac := auth.GetAuthContext(ctx)
    if ac == nil || ac.Type() == "" {
        return nil, false
    }
    return ac, true // *auth.AuthContext satisfies authz.Principal structurally
})
if err != nil {
    return fmt.Errorf("init authz: %w", err)
}

// Apply per route, after the authn middleware. Scope constants are app-defined.
mux.Handle("GET /api/v1/consignments",
    withAuth(authzr.RequireScope(scopes.ConsignmentRead)(
        http.HandlerFunc(consignmentRouter.HandleGetConsignments))))
```

## Service-layer checks

For logic beyond a coarse route gate, resolve the principal and inspect it:

```go
p, ok := authzr.Principal(ctx)
if !ok {
    return authz.ErrUnauthenticated
}
if !authz.HasScope(p, scopes.ConsignmentWrite) {
    return authz.ErrForbidden
}
// p.Roles() / p.Subject() are available for finer, business-specific decisions.
```
