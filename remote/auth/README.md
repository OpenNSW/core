# Auth Package

The `auth` package provides modular authentication strategies for the `remote` client.

## Authenticator Interface

All strategies implement the `Authenticator` interface, allowing them to be easily injected into any `remote.Client`.

```go
type Authenticator interface {
    Apply(req *http.Request) error
}
```

## Construction

There are two ways to build an authenticator:

1. **From config** — `auth.Build(cfg)` takes an `auth.Config`, resolves any secret
   references (see [Secret References](#secret-references)) once, and returns the
   authenticator. This is what the `remote` Manager uses for each service's `auth`
   block. A reference that cannot be resolved is a loud error.

   ```go
   authn, err := auth.Build(auth.Config{
       Type:   auth.TypeBearer,
       Bearer: &auth.BearerConfig{Token: "env:API_TOKEN"},
   })
   ```

   `Type` names the strategy, and the settings block of the same name — and only
   that block — must be set. A missing block, or a stray block for another
   strategy, is rejected rather than silently ignored.

2. **Directly** — the `New*` constructors take already-resolved plain values. They
   are pure: no I/O, no error.

   ```go
   auth.NewAPIKey("X-Custom-Key", "my-secret-key")
   auth.NewBearer("my-jwt-token")
   auth.NewOAuth2("https://identity.example.com/oauth2/token", "my-client-id", "my-client-secret", []string{"read", "write"})
   ```

`auth.Config` and the per-strategy settings structs carry `json` (snake_case) and
`yaml` (camelCase) tags, so they decode from either format; the package itself reads
no files.

## Supported Strategies

### API Key Authentication
Uses a custom header (e.g., `X-API-Key`) with a fixed value.

### Bearer Token Authentication
Uses the standard `Authorization: Bearer <token>` header.

### OAuth2 Client Credentials Flow
Implements the OAuth2 Client Credentials flow with the following features:
- Automatic token caching.
- Expiry handling with a 1-minute safety buffer.
- Synchronized token updates to prevent race conditions.
- Scope support.

## Secret References

Secret-bearing config fields (`value`, `token`, `client_secret`) are a
[`secret.SecretRef`](../../secret/README.md) — a literal value or a scheme-prefixed
reference (`env:NAME`, `file:/path`) that is resolved to its concrete value. The
type lives in the standalone [`secret`](../../secret/README.md) module; see its
README for the full scheme table and how to add a source.

A reference is written per field:

```yaml
auth:
  type: oauth2
  oauth2:
    tokenURL: https://identity.example.com/oauth2/token
    clientID: my-client
    clientSecret: env:CLIENT_SECRET
    scopes: [read, write]
```

References are resolved **once, at startup** — when `Manager.SetServices` is called,
or via `auth.Build`. A missing env var or an unreadable/empty file is a
**loud error** — a reference never silently resolves to the empty string.
Resolution is not repeated per request; if a referenced value changes, restart the
process to pick it up.

When loading with [`configyaml`](../../configyaml/README.md), write secret fields as
references (`env:NAME`), not `{{env:NAME}}` placeholders. A placeholder is expanded
before decoding, and the resulting value is then resolved again as a reference — so a
secret whose value happens to begin with `env:`, `file:` or `literal:` would be
misread.

## Strategy Configuration

### APIKeyConfig
Used when the authentication type is `"api_key"`; set under `api_key` (json) / `apiKey` (yaml).

| Field   | Type        | Description                                                                             |
|:--------|:------------|:----------------------------------------------------------------------------------------|
| `key`   | `string`    | The HTTP header name (e.g., `"X-API-Key"`).                                             |
| `value` | `SecretRef` | The key value — a literal or a reference (see [Secret References](#secret-references)). |

### BearerConfig
Used when the authentication type is `"bearer"`; set under `bearer`.

| Field   | Type        | Description                                                                                |
|:--------|:------------|:-------------------------------------------------------------------------------------------|
| `token` | `SecretRef` | The bearer token — a literal or a reference (see [Secret References](#secret-references)). |

### OAuth2Config
Used when the authentication type is `"oauth2"`; set under `oauth2`.

| Field (json / yaml)                                  | Type        | Description                                                                                                                                                                                                                                                                                                                                                                                          |
|:-----------------------------------------------------|:------------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `token_url` / `tokenURL`                             | `string`    | The OAuth2 token endpoint URL.                                                                                                                                                                                                                                                                                                                                                                       |
| `client_id` / `clientID`                             | `string`    | The client identifier.                                                                                                                                                                                                                                                                                                                                                                               |
| `client_secret` / `clientSecret`                     | `SecretRef` | The client secret — a literal or a reference (see [Secret References](#secret-references)).                                                                                                                                                                                                                                                                                                          |
| `scopes`                                             | `[]string`  | Optional list of requested scopes.                                                                                                                                                                                                                                                                                                                                                                   |
| `insecure_skip_tls_verify` / `insecureSkipTLSVerify` | `bool`      | Optional. Skips TLS certificate verification on the **token request** only. For local development against a self-signed identity provider — **never enable in production**. Defaults to `false`.                                                                                                                                                                                                     |
| `endpoint_params` / `endpointParams`                 | `object`    | Optional. Extra parameters for the token request, sent in the request body (RFC 6749 §3.2) — for example the RFC 8707 `resource` indicator naming the resource server a token is for. Each value is a string or an array of strings; `null` is rejected rather than sent as an empty parameter. `grant_type`, `scope`, `client_id` and `client_secret` are set by the flow itself and rejected here. |

```json
{
  "token_url": "https://idp.example/oauth2/token",
  "client_id": "my-client",
  "client_secret": "env:MY_CLIENT_SECRET",
  "scopes": ["api:read"],
  "endpoint_params": { "resource": "https://api.example" }
}
```

