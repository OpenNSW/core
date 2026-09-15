# xmlgen

Generates XML documents from a static template and structured data.

A template is the target XML with [`text/template`](https://pkg.go.dev/text/template) actions in
it, so it reads like the document it produces. Every value it prints is XML-escaped automatically,
numbers keep their exact text, and the result is checked as a document before it is returned.

## Quickstart

```go
tmpl := []byte(`<Declaration>
  <Ref>{{ .refNo }}</Ref>
  <Consignor>{{ .consignor }}</Consignor>
  {{- range .items }}
  <Item><Code>{{ .code }}</Code><Qty>{{ .qty }}</Qty></Item>
  {{- end }}
</Declaration>`)

data := []byte(`{"refNo":"A/1","consignor":"Smith & Co","items":[{"code":"X","qty":10000000}]}`)

doc, err := xmlgen.Generate(ctx, tmpl, data)
```

```xml
<Declaration>
  <Ref>A/1</Ref>
  <Consignor>Smith &amp; Co</Consignor>
  <Item><Code>X</Code><Qty>10000000</Qty></Item>
</Declaration>
```

Output is bytes, never a file. Stream it to a download, hand it to `storage`, or transmit it:

```go
w.Header().Set("Content-Type", "application/xml")
w.Header().Set("Content-Disposition", `attachment; filename="declaration.xml"`)
_ = xmlgen.GenerateTo(ctx, w, tmpl, data)
```

`GenerateTo` renders and validates in full before it writes anything, so a handler cannot emit half
a document after a 200.

## Data

`data` may be `[]byte` or `json.RawMessage`, decoded internally, or any value `text/template` can
walk: a `map[string]any`, a slice, or a struct addressed by **Go field name** (a `json` tag is not
consulted).

JSON input is decoded with `json.Decoder.UseNumber`, so numbers keep the text they were written
with — `10000000` stays `10000000` rather than becoming `1e+07`, `1.50` keeps its trailing zero,
and an integer beyond float precision stays exact. Hand over raw JSON bytes rather than an
already-decoded `map[string]any` whenever you can; the decoded path has already lost that.

A key the template references but the data does not have renders as the empty string, as does a
JSON `null`. `WithStrictKeys` turns the first into an error and is worth enabling in tests over a
template — it fires inside `{{ if }}` and `{{ range }}` too, so an optional field then needs a
guard such as `{{ if index . "middle_name" }}`.

A map or slice has no XML text form. Printing one is an error rather than Go syntax in the
document.

## Escaping

Escaping is not the template author's job. After parsing, xmlgen rewrites the template so every
printing action is piped through an escape function — `{{ .name }}` becomes `{{ .name | xml }}` —
which is the approach `html/template` takes to the same problem. Forgetting is not something a
template can do.

That covers places a data-side approach cannot reach: a `range` variable, a map key, the tail of a
pipeline, and a resolver's return value.

Escaping uses `encoding/xml.EscapeText`, which is correct in element content **and** in attribute
values, so one rule covers both. It renders a tab or newline as `&#x9;` / `&#xA;`; any conforming
parser reads those back as the original character, which is what keeps a multi-line address intact.

Opt out for a value that is already XML:

| | |
|---|---|
| `{{ raw .fragment }}` | emitted unescaped — the document check still applies, so a stray `&` is caught |
| `{{ cdata .body }}` | wrapped in CDATA, splitting any `]]>` in the value across two sections |

Text outside an action is never touched, which is how an empty-value marker is written:

```xml
<Gain>{{ if zero .gain }}<null/>{{ else }}{{ .gain }}{{ end }}</Gain>
```

> [!NOTE]
> Never substitute `html/template`. Its contextual escaping is HTML-specific and corrupts XML.
> An element or attribute *name* built from data is never safe: escaping applies to values, not to
> the markup around them.

## Helpers

Always available, pure, and deterministic.

| Helper | Example | Result |
|---|---|---|
| `part` | `{{ part .ref "/" 0 }}` on `"OFF1/A/42/2026"` | `OFF1` — past the end gives `""`, not an error |
| `split` | `{{ range split .codes "," }}` | iterate the fields |
| `join` | `{{ join .tags "-" }}` | `a-b-c` |
| `date` | `{{ date .day "2006-01-02" "1/2/06" }}` on `"2026-03-04"` | `3/4/26` — a value that does not match the input layout is an error, never a guess |
| `decimal` | `{{ decimal .fob 2 }}` on `1400` | `1400.00` |
| `lookup` | `{{ lookup .flag "yes" "1" "no" "0" }}` | `1` — an unmapped value passes through unchanged |
| `zero` | `{{ if zero .gain }}` | true for nil, `""`, `false`, and numeric zero |
| `coalesce` | `{{ coalesce .a .b "n/a" }}` | first value present |
| `trim` | `{{ trim .name }}` | surrounding whitespace removed |

`zero` exists because JSON numbers are carried as `json.Number`, which is a string underneath, so
`{{ if .quantity }}` is true even when the quantity is `0`. Use `{{ if zero .quantity }}`.

## Resolvers

Most templates need none — the helpers cover formatting. Supply a resolver for a value that cannot
be computed from the data, such as a lookup against a code list or another service:

```go
doc, err := xmlgen.Generate(ctx, tmpl, data, xmlgen.WithResolvers(xmlgen.Resolvers{
    "codelist": func(ctx context.Context, args ...any) (any, error) {
        return store.Describe(ctx, args[0].(string), args[1].(string))
    },
}))
```

```xml
<Origin>{{ codelist "country" .origin }}</Origin>
```

Each resolver is registered under its own name, so a template calling one that was not supplied
fails when the template is **parsed** — not at execution, if and when a branch happens to reach it.
Arguments arrive as the caller's own Go values (`string`, `json.Number`, `map[string]any`, `nil`),
and the return value is escaped like any other.

> [!IMPORTANT]
> A resolver must not have durable side effects. `text/template` calls a function once per
> *evaluated* occurrence and skips occurrences in branches it does not take, so minting a reference
> number inside `{{ if .isTransit }}…{{ end }}` would make the counter depend on the shape of the
> template, and a retried render would advance it again. Mint the number before calling `Generate`
> and put it in the data. If you must call one from a template, bind it once:
> `{{ $ref := refnum }}` and then use `{{ $ref }}`.

## Validation

Output is checked as a document before it is returned, and never rewritten — so the bytes stay
stable and remain valid to sign.

Beyond well-formedness, three checks `encoding/xml` does not make on its own: exactly one root
element, no `DOCTYPE`, and no namespace prefix that was never declared. The last matters because
`encoding/xml` documents that it does not reject an undefined prefix — it records the prefix as the
namespace — so a typo'd `soapp:Body` would otherwise reach the far end before anyone noticed.
`SkipNamespaceCheck` turns that off for a fragment whose declarations live elsewhere.

`Validate` parses a template without rendering it, for checking templates as they are stored or
sweeping a directory in CI, so a broken one fails at deploy rather than at submission.

## Errors

Every failure matches a sentinel with `errors.Is`: `ErrParseTemplate`, `ErrInvalidData`,
`ErrUnsupportedValue`, `ErrMissingKey`, `ErrHelper`, `ErrResolver`, `ErrRender`,
`ErrOutputTooLarge`, `ErrMalformedXML`, `ErrMultipleRoots`, `ErrDoctypeNotAllowed`,
`ErrUndeclaredNamespace`, `ErrInvalidResolverName`, `ErrReservedResolverName`. A resolver's own
error stays reachable, so `errors.Is(err, ErrResolver)` and `errors.Is(err, yourSentinel)` are both
true.

## Options

| Option | Effect |
|---|---|
| `WithResolvers(r)` | caller functions a template may call by name |
| `WithStrictKeys()` | a referenced-but-absent key becomes an error; for tests and CI |
| `WithMaxOutputBytes(n)` | caps the document; default 32 MiB, negative removes the cap |
| `SkipNamespaceCheck()` | allows undeclared prefixes |

## Testing

```sh
go test -race ./...
```
