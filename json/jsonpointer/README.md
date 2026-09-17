# jsonpointer

Resolve or set a value inside a nested `map[string]any` document by RFC 6901
JSON Pointer path (e.g. `/importer/address/district`).

## RFC 6901

Pointer syntax and escaping follow [RFC 6901](https://www.rfc-editor.org/rfc/rfc6901)
in full: a pointer is a sequence of `/`-prefixed segments, `~1` and `~0` decode
to `/` and `~` respectively, and the empty string denotes the whole document
(the root). This package treats the root pointer as invalid input, since `Get`
and `Set` exist to address a value *inside* a document, not the document
itself — see [Limitations](#limitations).

## Limitations

`Get` and `Set` only ever walk into `map[string]any`. They stop, without
error, the moment they meet anything else — including a `[]any` slice —
before the pointer is exhausted. So while the pointer *syntax* supports
array indices (e.g. `/items/0`), this package does not resolve them today:
`Get` on such a pointer reports `ok=false` once it reaches the slice.

Other current limitations:

- The empty (root) pointer is always rejected by `Valid`/`Segments`/`Get`/`Set`,
  even though RFC 6901 defines it as a legal pointer to the whole document.
- No `json.RawMessage`/`[]byte` convenience — callers must already have a
  decoded `map[string]any` (e.g. via `encoding/json` or `json.Decoder.UseNumber()`
  for numeric precision).

## Future plans

- **Array indexing.** Extending `Get`/`Set` to walk into `[]any` on a numeric
  segment (with RFC 6901's `-` meaning "one past the last element" for
  appends in `Set`). Nothing about the pointer syntax needs to change for
  this — only the walk in `Get`/`Set` — so existing pointers keep working
  unchanged once it lands.
- Revisiting root-pointer support if a caller needs to address the whole
  document rather than a field inside it.

## Usage

```go
import "github.com/OpenNSW/core/json/jsonpointer"

doc := map[string]any{
    "importer": map[string]any{
        "address": map[string]any{
            "district": "Gampaha",
        },
    },
}

v, ok := jsonpointer.Get(doc, "/importer/address/district") // "Gampaha", true

ok = jsonpointer.Set(doc, "/importer/address/postcode", "10230") // creates nothing new here, just adds a key
```

`Valid` checks pointer syntax without a document, for validating a pointer
at the time it's authored (e.g. in a config file) rather than the first time
it's used:

```go
jsonpointer.Valid("/importer/address/district") // true
jsonpointer.Valid("no-leading-slash")            // false
```

`Segments` splits a pointer into its unescaped path segments, for callers
building something other than a `Get`/`Set` call out of the path — e.g. a SQL
JSON-path expression:

```go
segments, err := jsonpointer.Segments("/importer/address/district")
// []string{"importer", "address", "district"}, nil
```
