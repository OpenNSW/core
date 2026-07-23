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

1. **From config** — `auth.Build(authType, options)` parses the JSON options for a
   strategy, resolves any secret references (see [Secret References](#secret-references))
   once, and returns the authenticator. This is what the `remote` Manager uses when
   it loads `services.json`. A reference that cannot be resolved is a loud error.

   ```go
   authn, err := auth.Build("oauth2", optionsJSON)
   ```

2. **Directly** — the `New*` constructors take already-resolved plain values. They
   are pure: no I/O, no error.

   ```go
   auth.NewAPIKey("X-Custom-Key", "my-secret-key")
   auth.NewBearer("my-jwt-token")
   auth.NewOAuth2("https://identity.example.com/oauth2/token", "my-client-id", "my-client-secret", []string{"read", "write"})
   ```

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

In `services.json`, a reference is written per field:

```json
{
  "auth": {
    "type": "oauth2",
    "options": {
      "token_url": "https://identity.example.com/oauth2/token",
      "client_id": "my-client",
      "client_secret": "env:CLIENT_SECRET",
      "scopes": ["read", "write"]
    }
  }
}
```

References are resolved **once, at startup** — when `Manager.LoadServices` loads the
file, or via `auth.Build`. A missing env var or an unreadable/empty file is a
**loud error** — a reference never silently resolves to the empty string.
Resolution is not repeated per request; if a referenced value changes, restart the
process to pick it up.

## Strategy Configuration

### APIKeyConfig
Used when the authentication type is `"api_key"`.

| Field   | Type        | Description                                                                             |
|:--------|:------------|:----------------------------------------------------------------------------------------|
| `key`   | `string`    | The HTTP header name (e.g., `"X-API-Key"`).                                             |
| `value` | `SecretRef` | The key value — a literal or a reference (see [Secret References](#secret-references)). |

### BearerConfig
Used when the authentication type is `"bearer"`.

| Field   | Type     | Description                                                                                |
|:--------|:---------|:-------------------------------------------------------------------------------------------|
| `token` | `SecretRef` | The bearer token — a literal or a reference (see [Secret References](#secret-references)). |

### OAuth2Config
Used when the authentication type is `"oauth2"`.

| Field           | Type       | Description                                                                                 |
|:----------------|:-----------|:--------------------------------------------------------------------------------------------|
| `token_url`     | `string`   | The OAuth2 token endpoint URL.                                                              |
| `client_id`     | `string`   | The client identifier.                                                                      |
| `client_secret` | `SecretRef` | The client secret — a literal or a reference (see [Secret References](#secret-references)). |
| `scopes`        | `[]string` | Optional list of requested scopes.                                                          |
| `insecure_skip_tls_verify` | `bool` | Optional. Skips TLS certificate verification on the **token request** only. For local development against a self-signed identity provider — **never enable in production**. Defaults to `false`. |

