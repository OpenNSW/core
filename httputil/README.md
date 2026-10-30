# httputil

Shared HTTP response helpers for writing JSON payloads and translatable, correlation-ID-tagged API error bodies. It has no dependencies — correlation ID extraction is an optional hook a consumer wires in, not a hard-coded package.

## Usage

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

### Errors

Errors are translatable: each has a stable code, and a message and description that each carry a key, an English default and named parameters. Clients show the English text as is, or look the key up in their own translations. The HTTP status comes from the error's category, never from the handler.

Declare each error once, as a package-level value:

```go
var ErrMemberNotFound = httputil.Error{
    Code:     "PB-1003", // an upper-case prefix per service or package, then 4 digits
    Category: httputil.CategoryNotFound,
    Message:  httputil.Message{Key: "pb.member.not_found", DefaultValue: "Member not found"},
    Description: httputil.Message{
        Key:          "pb.member.not_found_description",
        DefaultValue: "No member has the ID {{memberId}}",
    },
}
```

Return it as an ordinary `error` from anywhere, and write it with `WriteError`:

```go
func (s *Service) Member(ctx context.Context, id string) (*Member, error) {
    m, err := s.repo.Find(ctx, id)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, ErrMemberNotFound.WithParams(map[string]string{"memberId": id})
    }
    if err != nil {
        return nil, fmt.Errorf("find member %s: %w", id, err) // becomes ErrInternal
    }
    return m, nil
}

func handleGet(w http.ResponseWriter, r *http.Request) {
    m, err := svc.Member(r.Context(), r.PathValue("id"))
    if err != nil {
        httputil.WriteError(w, r, err)
        return
    }
    httputil.JSON(w, http.StatusOK, m)
}
```

The response is:

```json
{
  "code": "PB-1003",
  "message":     { "key": "pb.member.not_found", "defaultValue": "Member not found" },
  "description": { "key": "pb.member.not_found_description",
                   "defaultValue": "No member has the ID m-42",
                   "params": { "memberId": "m-42" } },
  "details": [],
  "correlationId": "c0ffee"
}
```

- **Parameters:** placeholders are `{{name}}`, the default syntax of i18next and similar libraries, so a frontend can pass `key` and `params` straight to its translator. `defaultValue` is sent with the placeholders filled in. `WithParams` gives each message only the params its text uses.
- **Categories:** `invalid` → 400, `unauthenticated` → 401, `forbidden` → 403, `notFound` → 404, `conflict` → 409, `tooLarge` → 413, `unsupportedMediaType` → 415, `unprocessable` → 422, `tooManyRequests` → 429, `unavailable` → 503, `internal` → 500. For any other status, declare a category once with `httputil.NewCategory("tooEarly", http.StatusTooEarly)`. A category without a 4xx or 5xx status is written as 500.
- **Server failures are safe by default:** any error that is not an `*httputil.Error` is logged with the correlation ID and written as `ErrInternal` (`SYS-5000`), so internal error text never reaches the client. Packages declare client errors only.
- **Causes:** `ErrX.Wrap(err)` keeps `err` for the log and sends only the declared messages. An `*Error` is logged when it carries a cause or extra context from wrapping: at error level for `internal` and `unavailable`, at warn level otherwise.
- **Matching:** `errors.Is(err, &ErrMemberNotFound)` matches the error however it was parameterized or wrapped, since errors compare by code. `errors.As` gives back the `*httputil.Error`.

#### Validation errors

Report every invalid field at once with `WithDetails`. Each `FieldError` has its own code and translatable message:

```go
return ErrInvalidRequest.WithDetails(
    httputil.FieldError{
        Field:   "contact.email",
        Code:    "PB-2001",
        Message: httputil.Message{Key: "pb.field.email_invalid", DefaultValue: "Enter a valid email address"},
    },
    httputil.FieldError{
        Field: "name",
        Code:  "PB-2002",
        Message: httputil.Message{Key: "pb.field.too_long", DefaultValue: "At most {{max}} characters"}.
            WithParams(map[string]string{"max": "64"}),
    },
)
```

#### Key catalog for translators

List a service's errors once. `Catalog` returns every key with its English default (including `ErrInternal`'s), and fails on a code or key declared twice with different content, a malformed code, a category without a 4xx or 5xx status or a missing key:

```go
var Errors = []httputil.Error{ErrMemberNotFound, ErrInvalidRequest /* ... */}

func TestErrorCatalog(t *testing.T) {
    if _, err := httputil.Catalog(Errors...); err != nil {
        t.Fatal(err)
    }
}
```

A `go generate` step can write the catalog as `en.json` for frontends to start translating from:

```go
catalog, err := httputil.Catalog(Errors...)
if err != nil {
    log.Fatal(err)
}
b, _ := json.MarshalIndent(catalog, "", "  ")
os.WriteFile("en.json", b, 0o644)
```

### Correlation IDs (optional)

`CorrelationIDFunc` is an optional `func(context.Context) string` hook, unset (`nil`) by default. When unset, `WriteError` simply omits `correlationId` from the response body. To populate it, wire in whatever correlation mechanism your service already uses — for example, `github.com/OpenNSW/core/trace`:

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
    // WriteError's log lines now get "traceId" attached automatically.
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
