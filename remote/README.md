# Auth Package

The `auth` package provides modular authentication strategies for the `remote` client.

## Authenticator Interface

All strategies implement the `Authenticator` interface, allowing them to be easily injected into any `remote.Client`.

```go
type Authenticator interface {
    Apply(req *http.Request) error
}
```

## Supported Strategies

### API Key Authentication
Uses a custom header (e.g., `X-API-Key`) with a fixed value.
```go
auth.NewAPIKey(auth.APIKeyConfig{
    Key:   "X-Custom-Key",
    Value: "my-secret-key",
})
```

### Bearer Token Authentication
Uses the standard `Authorization: Bearer <token>` header.
```go
auth.NewBearer(auth.BearerConfig{Token: "my-jwt-token"})
```

### OAuth2 Client Credentials Flow
Implements the OAuth2 Client Credentials flow with the following features:
- Automatic token caching.
- Expiry handling with a 1-minute safety buffer.
- Synchronized token updates to prevent race conditions.
- Scope support.

```go
auth.NewOAuth2(auth.OAuth2Config{
    TokenURL:     "https://identity.example.com/oauth2/token",
    ClientID:     "my-client-id",
    ClientSecret: "my-client-secret",
    Scopes:       []string{"read", "write"},
})
```

## Strategy Configuration

### APIKeyConfig
Used when the authentication type is `"api_key"`.

| Field | Type | Description |
| :--- | :--- | :--- |
| `key` | `string` | The HTTP header name (e.g., `"X-API-Key"`). |
| `value` | `string` | The static key value. |

### BearerConfig
Used when the authentication type is `"bearer"`.

| Field | Type | Description |
| :--- | :--- | :--- |
| `token` | `string` | The static bearer token. |

### OAuth2Config
Used when the authentication type is `"oauth2"`.

| Field | Type | Description |
| :--- | :--- | :--- |
| `token_url` | `string` | The OAuth2 token endpoint URL. |
| `client_id` | `string` | The client identifier. |
| `client_secret` | `string` | The client secret. |
| `scopes` | `[]string` | Optional list of requested scopes. |

