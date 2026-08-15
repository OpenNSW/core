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

## Request bodies

`Call` marshals `Request.Body` to JSON and decodes a JSON response. Two other body shapes are available for services that need them:

| Call | Body | Non-2xx |
|---|---|---|
| `Call` / `Client.JSONRequest` | JSON-marshalled `Body` | returned as an error, after decoding into `response` |
| `CallRaw` / `Client.RawRequest` | `Body` bytes sent verbatim (SOAP/XML) | returned in the response, **not** as an error |
| `CallMultipart` / `Client.MultipartRequest` | `multipart/form-data` parts | returned as an error, after decoding into `response` |

### multipart/form-data

For services that take a JSON document alongside file uploads. Parts are sent in the order given, which matters for receivers that pair a `fileN` part with an ordered list inside the payload:

```go
payload, err := remote.JSONPart("payload", application)
if err != nil {
    return err
}

var ack struct {
    ID     string `json:"id"`
    Status string `json:"status"`
}
err = manager.CallMultipart(ctx, "document-registry", remote.MultipartRequest{
    Method: http.MethodPost,
    Path:   "/api/documents/v1",
    Parts: []remote.Part{
        payload,
        {Name: "fileinfo", Content: []byte("1")},
        {Name: "file1", FileName: "invoice.pdf", ContentType: "application/pdf", Content: pdf},
    },
}, &ack)
```

A `Part` with an empty `FileName` is sent as a plain form field; setting `FileName` sends it as an uploaded file. `ContentType` is written as the part's own `Content-Type` header when set and omitted otherwise — some receivers tell a JSON part from a text field by that header alone, which is why `JSONPart` sets it for you.

The request `Content-Type` (including the generated boundary) is set by the client and cannot be overridden via `Headers`; supplying one would strip the boundary and leave the body unparseable. Parts are buffered in memory so the body can be replayed across retries — size uploads with that in mind.

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
