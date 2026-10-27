# telemetry

Shared OpenTelemetry setup for OpenNSW services: a tracer provider that always
creates spans and trace IDs, HTTP server and client instrumentation, and
metric, trace and log export over OTLP/HTTP when an endpoint is configured.

The OTel span's trace ID is the request's correlation ID. It is what logs,
error responses and the `X-Trace-ID` response header carry, and what a trace
backend shows for the request.

This module replaces [`trace`](../trace/README.md). Only services that import
it pull in the OTel SDK and exporters.

## Usage

```go
import (
    "context"
    "log/slog"
    "net/http"
    "os"

    "github.com/OpenNSW/core/httputil"
    "github.com/OpenNSW/core/telemetry"
    "github.com/OpenNSW/core/telemetry/logging"
)

func main() {
    ctx := context.Background()

    tel, err := telemetry.Start(ctx, telemetry.Config{
        Endpoint:    "http://otel-collector:4318", // empty: export off
        ServiceName: "my-service",
    })
    if err != nil {
        panic(err)
    }
    defer tel.Shutdown(ctx)

    // trace_id and span_id on every *Context log call made inside a request.
    slog.SetDefault(slog.New(logging.NewHandler(slog.NewJSONHandler(os.Stdout, nil))))

    // correlationId in error bodies is the trace ID.
    httputil.CorrelationIDFunc = telemetry.TraceID

    // Outgoing calls get a client span and carry traceparent.
    client := &http.Client{Transport: tel.Transport(nil)}
    _ = client

    mux := http.NewServeMux()
    mux.HandleFunc("GET /api/v1/resource/{id}", handleResource)

    http.ListenAndServe(":8080", tel.Handler("my-service", mux))
}
```

### Configuration

`Config` is the only source for the settings it has fields for. The matching
`OTEL_*` environment variables are ignored, so the config alone says where data
goes and an operator can't override it from the environment by accident.

| Field              | Notes                                                                                 | Ignored env var                         |
|--------------------|---------------------------------------------------------------------------------------|-----------------------------------------|
| `Endpoint`         | OTLP/HTTP base URL; `/v1/traces`, `/v1/metrics` and `/v1/logs` are appended           | `OTEL_EXPORTER_OTLP_ENDPOINT`           |
| `Traces.Endpoint`  | Full URL for traces, path included, used as-is; overrides `Endpoint` for traces       | `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`    |
| `Metrics.Endpoint` | Full URL for metrics, path included, used as-is; overrides `Endpoint` for metrics     | `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`   |
| `Logs.Endpoint`    | Full URL for logs, path included, used as-is; overrides `Endpoint` for logs           | `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`      |
| `ServiceName`      | `service.name` resource attribute; required when any signal is exported               | `OTEL_SERVICE_NAME`                     |
| `Headers`          | Sent with every export, e.g. `Authorization`                                          | `OTEL_EXPORTER_OTLP_HEADERS`            |

A signal is exported when it has its own endpoint or `Endpoint` is set, so a
service can export traces only, or send each signal to a different backend.
A signal's endpoint is a full URL: it can point at another service, or at
another path on the same host, but a bare path is rejected.

Settings `Config` has no field for are still read from the standard variables,
for example `OTEL_EXPORTER_OTLP_TIMEOUT`, `OTEL_EXPORTER_OTLP_COMPRESSION`, the
`OTEL_EXPORTER_OTLP_CERTIFICATE` family, `OTEL_TRACES_SAMPLER` and
`OTEL_RESOURCE_ATTRIBUTES` (except `service.name`).

`Config` has `yaml` tags, so it can be embedded in a service's config file and
loaded with [`configyaml`](../configyaml/README.md). To take a value from the
environment, reference it there explicitly:

```yaml
otel:
  endpoint: "{{env:OTEL_EXPORTER_OTLP_ENDPOINT}}"
  serviceName: my-service
  headers:
    Authorization: "Basic {{env:OTLP_BASIC_AUTH}}"
  traces:                       # optional: send traces somewhere else
    endpoint: http://tempo:4318/v1/traces
```

`Config.Validate` checks the endpoint URLs and the service name, so a service
can fail at config load time instead of at `Start`.

### Export off

With no endpoint, `Start` still installs a tracer provider. Every request gets a
span and a trace ID for logs, error bodies and `X-Trace-ID`; nothing is
exported. A signal that isn't exported records nothing: no metrics without a
metrics endpoint, and `SlogHandler` returns `nil` without a logs endpoint.

### Incoming `traceparent`

By default `Handler` treats its routes as public endpoints: each request starts
a new root span and an incoming `traceparent` is only linked. A caller can't
choose the trace ID or turn sampling off.

Routes reached only by services you control can instead continue the caller's
trace, so one request shows up as a single trace across services and every
service logs the same trace ID. Two options enable this:

| Option                           | Continues the caller's trace for        |
|----------------------------------|-----------------------------------------|
| `WithTrustedParent()`            | every request to the handler            |
| `WithTrustedParentFunc(trusted)` | requests for which `trusted(r)` is true |

The calling service sends requests through `Transport`, which adds
`traceparent` to each one:

```go
// orders service
client := &http.Client{Transport: tel.Transport(nil)}

req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://pricing:8080/internal/quote", body)
resp, err := client.Do(req) // carries traceparent from r's span
```

The called service serves internal and public routes from one mux and trusts
only the internal ones:

```go
// pricing service
mux := http.NewServeMux()
mux.HandleFunc("GET /api/v1/prices", listPrices)
mux.HandleFunc("POST /internal/quote", quote)

http.ListenAndServe(":8080", tel.Handler("pricing", mux,
    telemetry.WithTrustedParentFunc(func(r *http.Request) bool {
        return strings.HasPrefix(r.URL.Path, "/internal/")
    }),
))
```

`quote` runs in the orders service's trace: its span is a child of the orders
service's client span, and `telemetry.TraceID(r.Context())` returns the same ID
the orders service logged. `listPrices` still starts a new trace.

A handler that serves only internal routes can use `WithTrustedParent()`
instead of a function.

Keep in mind:

- **The function must check something an outside caller can't fake.** Whoever
  reaches a trusted request can set its trace ID and turn sampling off. A path
  prefix is only safe if outside traffic can't reach those paths, for example
  because the gateway in front blocks them. Stronger checks are a verified TLS
  client certificate (`r.TLS.VerifiedChains`) when services call each other
  over mutual TLS, or a header the gateway always strips from outside requests.
  `r.RemoteAddr` is not enough behind a proxy.
- **The function runs before the wrapped handler.** It sees the raw request
  (path, headers, TLS state) but not `r.Pattern`, and not anything middleware
  inside the handler adds, such as an authenticated identity.
- **A trusted request follows the caller's sampling decision.** If the caller
  didn't sample the request, it isn't recorded here either, but the trace ID
  still reaches logs and `X-Trace-ID`.
- **Only `traceparent` is read.** `X-Trace-ID` is a response header; a request's
  `X-Trace-ID` is ignored.

### Middleware and `http.route`

Make `Handler` the outermost layer, so other middleware runs inside the span
and its logs carry the trace ID. If any of that middleware replaces the request
(`r = r.WithContext(...)`, common in authentication and request logging), the
mux sets the matched pattern on a copy `Handler` never sees, and metrics lose
`http.route`. Wrap the mux with `Routes` to keep it:

```go
handler := tel.Handler("my-service",
    requestLog(cors(telemetry.Routes(mux))),
)
```

`Routes` is only needed when middleware sits between `Handler` and the mux;
`Handler(…, mux)` tags the route itself. Outside a `Handler`, `Routes` just
calls the mux.

### Log export

When logs are exported, `SlogHandler` returns an `slog.Handler` that sends records
over OTLP. Fan out to it beside the stdout handler if logs should reach the
backend too. Wrap only the stdout handler with `logging.NewHandler`: OTLP log
records already carry the span context.

## Behavior

- **HTTP metrics.** `http.route` is taken from the `ServeMux` pattern (method
  and host stripped), since otelhttp doesn't set it on metrics. Routers that
  don't set `r.Pattern` get no route label.
  `server.address` and `server.port` are dropped from `http.server.*` metrics
  because otelhttp reads them from the client-controlled `Host` header.
- **Globals.** `Start` registers the tracer provider and the W3C trace context
  propagator globally, and the meter provider when metrics are exported, so
  `otel.Tracer` and `otel.Meter` work anywhere in the service.
- **`X-Trace-ID`.** `Handler` sets the response header to the request's trace
  ID before calling the wrapped handler.

## Testing

Run the package tests (from within the `telemetry` directory):

```bash
go test ./...
```
