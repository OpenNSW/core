# trace

A lightweight Go package providing HTTP request correlation, trace ID generation, and context-based correlation for logs and audit records.

## Usage

### Injecting Trace Middleware

Integrate `TraceMiddleware` into your HTTP handler pipeline. The middleware always assigns a **server-generated** correlation id. When OpenTelemetry has already started a server span (for example via `otelhttp`), that span's TraceID is used so logs and spans match; otherwise a random 32-character hex id is generated.

Caller headers (`X-Trace-ID`, `X-Correlation-ID`, `X-Request-ID`) are **not** used as the correlation id. Valid values are kept only as `clientRequestId` for matching the caller's own records.

```go
import (
    "net/http"
    "github.com/OpenNSW/core/trace"
)

func main() {
    mux := http.NewServeMux()
    mux.HandleFunc("/api/v1/resource", handleResource)

    // When using otelhttp, wrap TraceMiddleware *inside* it so the span exists:
    //   otelhttp.NewHandler(trace.TraceMiddleware(mux), "api")
    // If a log middleware sits outside otelhttp and logs with the outer request
    // context after the handler, wrap with TraceSlotMiddleware so GetTraceID
    // still sees the id TraceMiddleware wrote:
    //   trace.TraceSlotMiddleware(systemlog(otelhttp(trace.TraceMiddleware(mux))))
    handler := trace.TraceMiddleware(mux)

    http.ListenAndServe(":8080", handler)
}
```

### Logging with Trace IDs

Wrap an `slog.Handler` with `logging.NewHandler` (in the `trace/logging` subpackage) to automatically attach `"traceId"` on every log record produced via a `*Context` slog method. When the caller supplied a request id header, `"clientRequestId"` is attached as well.

```go
import (
    "log/slog"
    "net/http"
    "os"

    "github.com/OpenNSW/core/trace"
    "github.com/OpenNSW/core/trace/logging"
)

func main() {
    base := slog.NewJSONHandler(os.Stdout, nil)
    logger := slog.New(logging.NewHandler(base))

    mux := http.NewServeMux()
    mux.HandleFunc("/api/v1/resource", func(w http.ResponseWriter, r *http.Request) {
        logger.ErrorContext(r.Context(), "request failed") // includes "traceId"
    })

    http.ListenAndServe(":8080", trace.TraceMiddleware(mux))
}
```

### Context Helpers

```go
import (
    "fmt"
    "github.com/OpenNSW/core/trace"
)

func process(ctx context.Context) {
    if id := trace.GetTraceID(ctx); id != "" {
        fmt.Printf("server correlation id: %s\n", id)
    }
    if id := trace.GetClientRequestID(ctx); id != "" {
        fmt.Printf("caller request id: %s\n", id)
    }
}
```

## Behavior

### 1. Server correlation id

`TraceMiddleware` never trusts the caller for the correlation id:

1. If the request context has a valid OpenTelemetry `SpanContext`, use its `TraceID` (32 lowercase hex characters).
2. Otherwise generate a cryptographically secure random 16-byte id, formatted as 32 hex characters.

That value is set on the context (`GetTraceID`), returned as the `X-Trace-ID` response header, and (when `TraceSlotMiddleware` is used) written into the outer slot.

### 2. Caller request id (information only)

The first valid value among `X-Trace-ID`, `X-Correlation-ID`, and `X-Request-ID` is stored as `GetClientRequestID` / log field `clientRequestId`. Validation: length 1–64; characters `[A-Za-z0-9-_:./=]` only. Invalid values are discarded. This id is never used as `traceId` or `X-Trace-ID` on the response.

### 3. Breaking change from v0.2.0

A caller's `X-Trace-ID` (or correlation / request id header) is no longer echoed as the response `X-Trace-ID` and no longer becomes the log/audit `traceId`.

## Testing

Run the package tests using (from within the `trace` directory):

```bash
go test ./...
```
