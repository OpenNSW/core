# trace

A lightweight Go package providing HTTP request trace propagation, trace ID generation, and context-based trace ID correlation.

## Usage

### Injecting Trace Middleware

Integrate `TraceMiddleware` into your HTTP handler pipeline to automatically extract, validate, and propagate request trace IDs:

```go
import (
    "net/http"
    "github.com/OpenNSW/core/trace"
)

func main() {
    mux := http.NewServeMux()
    mux.HandleFunc("/api/v1/resource", handleResource)

    // Wrap the mux with the trace middleware
    handler := trace.TraceMiddleware(mux)

    http.ListenAndServe(":8080", handler)
}
```

### Context Helpers

You can manually get or set the trace ID within a `context.Context` using the provided helpers:

```go
import (
    "context"
    "fmt"
    "github.com/OpenNSW/core/trace"
)

func process(ctx context.Context) {
    // Extract the trace ID from the context
    traceID := trace.GetTraceID(ctx)
    if traceID != "" {
        fmt.Printf("Processing request with Trace ID: %s\n", traceID)
    }

    // Inject a trace ID into a new context
    newCtx := trace.ContextWithTraceID(ctx, "custom-trace-id")
    _ = newCtx
}
```

## Behavior

### 1. Header Extraction & Precedence
The middleware inspects incoming request headers in the following order to resolve a trace or correlation ID:
1. `X-Trace-ID`
2. `X-Correlation-ID`
3. `X-Request-ID`

The first non-empty header value found is selected as the trace ID candidate.

### 2. Validation Constraints
To prevent header injection or trace ID corruption, resolved candidate IDs are validated. A valid trace ID:
- Must have a length between 1 and 64 characters.
- Must contain only alphanumeric characters (`a-zA-Z0-9`) or the following safe special characters: `-`, `_`, `:`, `.`, `/`, `=`.

If the candidate ID fails validation, it is discarded.

### 3. Fallback Generation
If no candidate ID is found in the headers, or if the candidate ID is invalid, the middleware automatically generates a fallback trace ID using a cryptographically secure random 16-byte array, formatted as a 32-character hexadecimal string.

### 4. Response Header Propagation
Once validated or generated, the trace ID is injected into the response headers under:
- `X-Trace-ID`

This ensures that the client receives the exact trace ID that was used to process their request, facilitating end-to-end debugging.

## Testing

Run the package tests using:

```bash
go test ./trace/...
```
