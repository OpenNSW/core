# remote

Registry-based outbound HTTP client manager. Services (external agencies, backend APIs) are declared in a JSON config file; the manager resolves endpoints, applies authentication, and executes requests — no per-service boilerplate in your application code.

## Usage

```go
import "github.com/OpenNSW/core/remote"

manager := remote.NewManager()
if err := manager.LoadServices("configs/services.json"); err != nil {
    log.Fatal(err)
}

// Call a registered service
var result MyResponseType
err := manager.Call(ctx, "npqs-api", remote.Request{
    Method: http.MethodPost,
    Path:   "/v1/applications",
    Body:   myPayload,
}, &result)
```

## Services config

`services.json` declares available services with their endpoint, timeout, and authentication:

```json
{
  "version": "1",
  "services": [
    {
      "id": "npqs-api",
      "url": "https://npqs.example.gov/api",
      "timeout_seconds": 30,
      "auth": {
        "type": "oauth2",
        "options": {
          "token_url": "https://idp.example.gov/token",
          "client_id": "my-client",
          "client_secret": "secret",
          "scopes": ["npqs:submit"]
        }
      }
    },
    {
      "id": "legacy-api",
      "url": "https://legacy.example.gov",
      "timeout_seconds": 10,
      "auth": {
        "type": "api_key",
        "options": {
          "key": "X-API-Key",
          "value": "my-api-key"
        }
      }
    }
  ]
}
```

## Authentication strategies

See [`remote/auth`](auth/README.md) for the full reference. Supported types:

| `type` | Description |
|---|---|
| `api_key` | Static header (e.g. `X-API-Key: value`) |
| `bearer` | `Authorization: Bearer <token>` |
| `oauth2` | Client credentials flow with automatic token caching |

## Direct client access

```go
client, err := manager.GetClient("npqs-api")

// Execute a pre-built *http.Request directly
resp, err := client.Do(req)
```

## Listing registered services

```go
ids := manager.ListServices() // []string{"npqs-api", "legacy-api"}
```
