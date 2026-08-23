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
    Body:   remote.JSONBody{V: myPayload},
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

## Headers a caller resolves per call

`Request.Headers` covers a call that knows its own headers. When the code holding
the value is not the code building the request — a generic plugin assembling a
request from a template, an identifier looked up for the case being processed —
put them on the context instead:

```go
ctx = remote.ContextWithHeaders(ctx, map[string]string{"X-Filed-For": orgID})
```

Every request made with that context carries them, whatever body it sends.
Precedence, weakest first: context headers, `Request.Headers`, then the
authenticator. So a call that does model its headers still wins, and no context
value can displace authentication.

A caller whose values arrive untyped — decoded from JSON, or assembled by an
engine from a template — hands them over as they are:

```go
ctx, err := remote.ContextWithHeaderValues(ctx, values) // map[string]any
```

A value that is not a string, or an empty header name, is an error: whatever built
the map named a header that cannot be sent, and hearing that from the service is
worse. A value that is present but empty is dropped with a warning instead, so an
optional source that resolved to blank still makes its call.

## Authentication strategies

See [`remote/auth`](auth/README.md) for the full reference. Supported types:

| `type` | Description |
|---|---|
| `api_key` | Static header (e.g. `X-API-Key: value`) |
| `bearer` | `Authorization: Bearer <token>` |
| `oauth2` | Client credentials flow with automatic token caching |

## Request bodies

`Request.Body` is a `remote.Body` — an interface that encodes itself into wire bytes plus the `Content-Type` header describing them. Built-in implementations:

| Body                         | Encodes as                                               |
|------------------------------|----------------------------------------------------------|
| `JSONBody{V}`                | JSON-marshalled `V`                                      |
| `RawBody{Data, ContentType}` | `Data` sent verbatim under `ContentType` (e.g. SOAP/XML) |
| `FormBody{Values}`           | `application/x-www-form-urlencoded`                      |
| `MultipartBody{Parts}`       | `multipart/form-data`                                    |

A nil `Body` sends no request body at all (e.g. a plain `GET`).

Two calls consume a `Request`, differing only in how they treat the response:

| Call                            | Response handling                                                                        |
|---------------------------------|------------------------------------------------------------------------------------------|
| `Call` / `Client.Request`       | decodes a JSON response into `response`; non-2xx is returned as an error, after decoding |
| `CallRaw` / `Client.RawRequest` | returns the raw, undecoded response; non-2xx is **not** an error                         |

`MultipartBody` decodes and errors the same way `JSONBody` does, so multipart calls go through `Call` / `Client.Request` too — there is no separate multipart call.

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
err = manager.Call(ctx, "document-registry", remote.Request{
    Method: http.MethodPost,
    Path:   "/api/documents/v1",
    Body: remote.MultipartBody{Parts: []remote.Part{
        payload,
        {Name: "fileinfo", Content: []byte("1")},
        {Name: "file1", FileName: "invoice.pdf", ContentType: "application/pdf", Content: pdf},
    }},
}, &ack)
```

A `Part` with an empty `FileName` is sent as a plain form field; setting `FileName` sends it as an uploaded file. `ContentType` is written as the part's own `Content-Type` header when set and omitted otherwise — some receivers tell a JSON part from a text field by that header alone, which is why `JSONPart` sets it for you.

The request `Content-Type` (including the generated boundary) is set by the client and cannot be overridden via `Headers`; supplying one would strip the boundary and leave the body unparseable. Parts are buffered in memory so the body can be replayed across retries — size uploads with that in mind.

## Direct client access

```go
client, err := manager.GetClient("npqs-api")

var result MyResponseType
err = client.Request(ctx, remote.Request{Method: http.MethodGet, Path: "/v1/applications/123"}, &result)
```

## Listing registered services

```go
ids := manager.ListServices() // []string{"npqs-api", "legacy-api"}
```
