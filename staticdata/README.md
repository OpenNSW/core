# staticdata

Serves large, versioned JSON reference data, such as HS codes, ports or country lists, by ID. The data ships as
`static_data` artifacts through the [artifact registry](../artifact/README.md) rather than being bundled into a portal
build or embedded in the server. A portal reads it over HTTP; server-side code, such as a document that prints a
stored code's title, reads it with `LoadAt` and `Find`.

## Artifact format

A `static_data` artifact is a JSON object with a top-level `data` array. A bare array is refused, so fields can be
added to the envelope later without breaking consumers.

```json
{
  "data": [
    { "const": "LK", "title": "Sri Lanka" },
    { "const": "IN", "title": "India", "parents": ["SAARC"] }
  ]
}
```

Each row needs a non-empty string `const` (the stored value) and `title` (what a person sees). Any other keys are kept
and returned. `parents` lists the values of another field a row belongs to, for dependent dropdowns.

`static_data` is its own artifact kind, so registering an ID here never exposes an internal artifact, such as a
template or workflow definition, on the public endpoint.

## HTTP

```go
h := staticdata.NewHandler(artifactSet)
mux.Handle("GET /api/v1/static-data/{id}", withAuth(http.HandlerFunc(h.HandleGet)))
```

`GET /api/v1/static-data/{id}?manifestVersion=N`

- `manifestVersion` is required: a case pins the manifest version it starts on, so its forms keep the data they
  opened with while a later manifest ships a new file under the same ID.
- Without `q`, `offset` or `limit`, it returns the artifact as stored.
- With any of them, it returns one page (a [`pagination`](../pagination/README.md) envelope) of matching rows:
  - `q` ranks rows by case-insensitive match on `searchKeys` (comma-separated row keys; default `title,const`). An
    empty `q` keeps artifact order.
  - `parent` keeps only rows whose `parents` include it.
- An unknown ID or manifest version is a 404.

Responses are cached as `private, max-age=31536000, immutable`: a manifest version's data never changes, and the
endpoint is behind authentication, so no shared cache may serve it.

## In Go

```go
raw, err := staticdata.LoadAt(ctx, artifactSet, "countries", manifestVersion)

opt, found, err := staticdata.Find(raw, "LK") // exact const match: opt.Title == "Sri Lanka"

page, err := staticdata.Search(raw, "lan", "", nil, 0, 20) // what the handler runs for a search
```
