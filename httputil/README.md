# httputil

Shared HTTP helpers for reading JSON request bodies with a size limit, and for writing JSON payloads and standardized, correlation-ID-tagged API error bodies. It has no dependencies — correlation ID extraction is an optional hook a consumer wires in, not a hard-coded package.

## Usage

### Reading JSON request bodies

`DecodeJSON` reads the request body as exactly one JSON value into a destination, reading at most `limit` bytes. It refuses a field the destination does not declare and anything after the value, so a misspelt key is reported instead of silently dropped. A map or `json.RawMessage` field still takes any keys.

It writes nothing itself. `WriteDecodeError` answers its error with `Error`: 413 `request body too large` for a body over the limit, otherwise 400 `invalid request body`, naming the field when it is an unknown one.

```go
type createItemRequest struct {
    Name string `json:"name"`
}

func handleCreate(w http.ResponseWriter, r *http.Request) {
    var req createItemRequest
    if err := httputil.DecodeJSON(w, r, 1<<10, &req); err != nil {
        httputil.WriteDecodeError(w, r, err)
        return
    }
    // ...
}
```

To answer a decode error yourself, for example to audit it, match it with `errors.Is` against `ErrBodyTooLarge`, `ErrBodyEmpty` (an empty body, for routes where the body is optional) and `ErrBodyInvalid` (malformed JSON, a wrong type, trailing data, or an `*UnknownFieldError`), and respond with `MsgBodyTooLarge` or `MsgInvalidBody` so every endpoint describes a bad body alike.

### Writing JSON responses

```go
import (
    "net/http"

    "github.com/OpenNSW/core/httputil"
)

func handleGet(w http.ResponseWriter, r *http.Request) {
    httputil.JSON(w, http.StatusOK, map[string]string{"id": "c-1"})
}
```

### Client-facing errors

`Error` writes a fixed, safe message alongside the request's correlation ID (see "Correlation IDs" below), so the client can report the ID back for support without ever seeing internal error details:

```go
func handleGet(w http.ResponseWriter, r *http.Request) {
    item, ok := repo.Find(r.Context(), id)
    if !ok {
        httputil.Error(w, r, http.StatusNotFound, "item not found")
        return
    }
    httputil.JSON(w, http.StatusOK, item)
}
```

### Server-side errors

`InternalServerError` logs the real error server-side (via `slog`) and responds with a generic, safe message to the client:

```go
func handleGet(w http.ResponseWriter, r *http.Request) {
    item, err := repo.Find(r.Context(), id)
    if err != nil {
        httputil.InternalServerError(w, r, "failed to load item", err, "itemId", id)
        return
    }
    httputil.JSON(w, http.StatusOK, item)
}
```

### Correlation IDs (optional)

`CorrelationIDFunc` is an optional `func(context.Context) string` hook, unset (`nil`) by default. When unset, `Error`/`InternalServerError` simply omit `correlationId` from the response body. To populate it, wire in whatever correlation mechanism your service already uses — for example, `github.com/OpenNSW/core/trace`:

```go
import (
    "github.com/OpenNSW/core/httputil"
    "github.com/OpenNSW/core/trace"
)

func main() {
    httputil.CorrelationIDFunc = trace.GetTraceID
    // ...
}
```

Combine it with `trace/logging.NewHandler` so the same ID that's returned to the client as `correlationId` also shows up on server-side log lines as `traceId`:

```go
import (
    "log/slog"
    "os"

    "github.com/OpenNSW/core/httputil"
    "github.com/OpenNSW/core/trace"
    "github.com/OpenNSW/core/trace/logging"
)

func main() {
    httputil.CorrelationIDFunc = trace.GetTraceID
    slog.SetDefault(slog.New(logging.NewHandler(slog.NewJSONHandler(os.Stdout, nil))))
    // InternalServerError's slog.ErrorContext calls now get "traceId" attached automatically.
}
```

You can also read the correlation ID directly:

```go
func handleGet(w http.ResponseWriter, r *http.Request) {
    correlationID := httputil.CorrelationID(r)
    // ...
}
```

## Testing

Run the package tests using (from within the `httputil` directory):

```bash
go test ./...
```
