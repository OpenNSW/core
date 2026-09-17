# json

`github.com/OpenNSW/core/json` groups packages for working with decoded JSON
documents (`map[string]any` / `[]any` trees) — addressing, reshaping, or
otherwise operating on data after it's been unmarshalled, as opposed to
defining wire schemas.

## `jsonpointer`

Resolve or set a value inside a nested `map[string]any` document by RFC 6901
JSON Pointer path. See [jsonpointer/README.md](jsonpointer/README.md).
