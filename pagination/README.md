# pagination

Standard pagination envelope and query parameter parsing. Keeps list endpoints consistent across the API.

## Usage

```go
import "github.com/OpenNSW/core/pagination"

func handleList(w http.ResponseWriter, r *http.Request) {
    rawOffset, rawLimit, err := pagination.ParsePaginationParams(r)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    offset, limit := pagination.ResolvePaginationParams(rawOffset, rawLimit)
    items, total := repo.List(r.Context(), offset, limit)
    page := pagination.NewPageResult(items, total, offset, limit)

    json.NewEncoder(w).Encode(page)
}
```

`ParsePaginationParams` reads `?offset=` and `?limit=` from the query string and returns pointers — `nil` means the parameter was absent, letting you apply defaults via `ResolvePaginationParams`.

## Defaults and limits

```go
// Apply defaults and enforce the max limit cap without parsing HTTP params
offset, limit := pagination.ResolvePaginationParams(rawOffset, rawLimit)
```

| Parameter | Default | Max |
|---|---|---|
| `offset` | `0` | — |
| `limit` | `50` | `100` |

## Response envelope

`Page[T]` is the JSON envelope returned to clients:

```json
{
  "items": [...],
  "total": 247,
  "offset": 50,
  "limit": 50
}
```

`NewPageResult` normalises a `nil` items slice to an empty array so the JSON response always contains `"items": []` rather than `"items": null`.
