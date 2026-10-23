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

1. **From config** — `auth.Build(cfg)` takes an `auth.Config`, validates it, and
   returns the authenticator. This is what the `remote` Manager uses for each
   service's `auth` block.

   ```go
   authn, err := auth.Build(auth.Config{
       Type:   auth.TypeBearer,
       Bearer: &auth.BearerConfig{Token: os.Getenv("API_TOKEN")},
   })
   ```

   `Type` names the strategy, and the settings block of the same name — and only
   that block — must be set. A missing block, or a stray block for another
   strategy, is rejected rather than silently ignored.

2. **Directly** — the `New*` constructors take the same plain values. They are
   pure: no I/O, no error.

   ```go
   auth.NewAPIKey("X-Custom-Key", "my-secret-key")
   auth.NewBearer("my-jwt-token")
   auth.NewOAuth2("https://identity.example.com/oauth2/token", "my-client-id", "my-client-secret", []string{"read", "write"})
   ```

`auth.Config` and the per-strategy settings structs carry `json` and `yaml` tags with
the same camelCase keys, so they decode from either format; the package itself reads
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

## Secrets

Secret-bearing fields (`value`, `token`, `clientSecret`) are plain strings. With
[`configyaml`](../../configyaml/README.md), write them as placeholders, which are
expanded while the file is loaded:

```yaml
auth:
  type: oauth2
  oauth2:
    tokenURL: https://identity.example.com/oauth2/token
    clientID: my-client
    clientSecret: "{{env:CLIENT_SECRET}}"
    scopes: [read, write]
```

## Strategy Configuration

### APIKeyConfig
Used when the authentication type is `"apiKey"`; set under `apiKey`.

| Field   | Type        | Description                                                                             |
|:--------|:------------|:----------------------------------------------------------------------------------------|
| `key`   | `string`    | The HTTP header name (e.g., `"X-API-Key"`).                                             |
| `value` | `string`    | The key value.                                                                          |

### BearerConfig
Used when the authentication type is `"bearer"`; set under `bearer`.

| Field   | Type        | Description                                                                                |
|:--------|:------------|:-------------------------------------------------------------------------------------------|
| `token` | `string`    | The bearer token.                                                                          |

### OAuth2Config
Used when the authentication type is `"oauth2"`; set under `oauth2`.

| Field                   | Type        | Description                                                                                                                                                                                                                                                                                                                                                                                          |
|:------------------------|:------------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `tokenURL`              | `string`    | The OAuth2 token endpoint URL.                                                                                                                                                                                                                                                                                                                                                                       |
| `clientID`              | `string`    | The client identifier.                                                                                                                                                                                                                                                                                                                                                                               |
| `clientSecret`          | `string`    | The client secret.                                                                                                                                                                                                                                                                                                                                                                                   |
| `scopes`                | `[]string`  | Optional list of requested scopes.                                                                                                                                                                                                                                                                                                                                                                   |
| `insecureSkipTLSVerify` | `bool`      | Optional. Skips TLS certificate verification on the **token request** only. For local development against a self-signed identity provider — **never enable in production**. Defaults to `false`.                                                                                                                                                                                                     |
| `endpointParams`        | `object`    | Optional. Extra parameters for the token request, sent in the request body (RFC 6749 §3.2) — for example the RFC 8707 `resource` indicator naming the resource server a token is for. Each value is a string or an array of strings; `null` is rejected rather than sent as an empty parameter. `grant_type`, `scope`, `client_id` and `client_secret` are set by the flow itself and rejected here. |

```json
{
  "tokenURL": "https://idp.example/oauth2/token",
  "clientID": "my-client",
  "clientSecret": "<client secret>",
  "scopes": ["api:read"],
  "endpointParams": { "resource": "https://api.example" }
}
```

