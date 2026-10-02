# htmlgen

Generates print-ready HTML documents — permits, licences, certificates, receipts — from a static template and
structured data.

A template is the target HTML with [`html/template`](https://pkg.go.dev/html/template) actions in
it, so it reads like the document it produces. Every value it prints is escaped for the context it
appears in, numbers keep their exact text, and a value html/template refuses fails the render
instead of reaching the document. It is the HTML sibling of [`xmlgen`](../xmlgen/README.md): same
shape, same helpers, same rules for values.

htmlgen does not produce PDF. Its output is an HTML document with print CSS; a browser prints it
(or saves it as PDF), and a server-side converter can turn the same bytes into a file.

## Quickstart

```go
tmpl := []byte(`<!DOCTYPE html>
<html><head><style>@page { size: A4; margin: 12mm; }</style></head>
<body>
  <h1>Permit</h1>
  <p>Permit No: {{ .permit_number }}</p>
  <p>Applicant: {{ .application.applicant_name }}</p>
  <p>Fee paid: {{ decimal .application.fee_amount 2 }}</p>
  <p>Issued on {{ date .issued_at "2006-01-02" "02/01/2006" }}</p>
</body></html>`)

data := []byte(`{
  "permit_number": "PRM-2026-00555",
  "issued_at": "2026-10-01",
  "application": { "applicant_name": "Smith & Co", "fee_amount": 10000000 }
}`)

doc, err := htmlgen.Generate(ctx, tmpl, data)
```

```html
  <p>Permit No: PRM-2026-00555</p>
  <p>Applicant: Smith &amp; Co</p>
  <p>Fee paid: 10000000.00</p>
  <p>Issued on 01/10/2026</p>
```

Output is bytes, never a file. Return it to a browser to preview and print, hand it to `storage`,
or pass it to a PDF converter:

```go
w.Header().Set("Content-Type", "text/html; charset=utf-8")
_ = htmlgen.GenerateTo(ctx, w, tmpl, data)
```

`GenerateTo` renders and checks in full before it writes anything, so a handler cannot emit half a
document after a 200.

## Data first

The caller assembles **everything** the document needs into `data` before calling `Generate`; the
template only arranges and formats it. Fields gathered from several sources — what an
applicant submitted, what a reviewer decided, values entered for this document — become nested
objects in one document, and the template addresses them by path:

```html
<td>{{ .application.applicant_name }}</td>
<td>{{ .review.reference_number }}</td>
<td>{{ .permit_number }}</td>
```

Keeping data access out of the template keeps it in code the caller can authorize, test and log,
and makes a render reproducible: the same template and data always give the same bytes. That
includes dates — there is no `today` helper. Put the issue date in the data (`"issued_at":
"2026-10-01"`), in the timezone the document should state, and format it with `date`.

## Data

`data` may be `[]byte` or `json.RawMessage`, decoded internally, or any value `html/template` can
walk: a `map[string]any`, a slice, or a struct addressed by **Go field name** (a `json` tag is not
consulted).

JSON input is decoded with `json.Decoder.UseNumber`, so numbers keep the text they were written
with — `10000000` stays `10000000` rather than becoming `1e+07`, `1.50` keeps its trailing zero,
and an integer beyond float precision stays exact. Hand over raw JSON bytes rather than an
already-decoded `map[string]any` whenever you can; the decoded path has already lost that.

A key the template references but the data does not have renders as the empty string, as does a
JSON `null` and a path through a missing object (`{{ .a.b.c }}`). `WithStrictKeys` turns the first
into an error and is worth enabling in tests over a template — it fires inside `{{ if }}` and
`{{ range }}` too, so an optional field then needs a guard such as `{{ if index . "middle_name" }}`.

A map or slice has no text form. Printing one is an error rather than Go syntax in the document.

## Escaping

Escaping is `html/template`'s contextual escaping, unchanged. A value is escaped for where it is
printed — element text, a quoted attribute, a URL, a `<script>` or `<style>` block — so markup in
the data cannot forge elements and a quote cannot break out of an attribute. Escaping reaches the
bodies of `{{ define }}` and `{{ block }}`, range variables and map keys alike.

Two refusals are surfaced as errors instead of being left in the document:

| Error | Cause |
|---|---|
| `ErrUnsafeValue` | html/template rejected a value for its context — a `javascript:` URL in an `href`, a `data:` URL from the data in an `img src` — and printed its `ZgotmplZ` marker in its place |
| `ErrUnsafeTemplate` | html/template cannot work out a context — an `{{ if }}` whose branches end in different contexts, or a template that ends inside a tag or quoted attribute |

Before a value is escaped, htmlgen converts it to text by the same rules as xmlgen: `{{ .name }}`
is rewritten to `{{ .name | text }}`, and html/template appends its escaper after that. You never
need to write `text` yourself.

Never write `html`, `js` or `urlquery` either. The value is already escaped for where it appears,
so a second escaper would escape it twice — `{{ .ref | js }}` in a script would put the escape
sequences themselves into the string. Templates that call one are rejected with
`ErrUnsupportedTemplate`, by `Validate` as well as `Generate`.

### Trusted values

A caller that needs to print trusted markup, or an image as a `data:` URL, passes it from **Go
code** as one of html/template's typed strings. They pass through unchanged:

```go
data := map[string]any{
    "emblem":    template.URL("data:image/png;base64," + emblemPNG),
    "signature": template.HTML(signatureSVG),
}
```

```html
<img src="{{ .emblem }}"> {{ .signature }}
```

JSON data never decodes to these types, so nothing a user submitted can opt itself out of escaping.
Static assets such as an emblem can also be written straight into the template as an inline
`<svg>` or a literal `data:` URL; text outside an action is emitted verbatim.

> [!WARNING]
> `template.HTML` and its siblings are a genuine hole. Use them only for values your own code
> produced, never for a string that came from a request.

## Helpers

Always available, pure, and deterministic. They are identical to xmlgen's.

| Helper | Example | Result |
|---|---|---|
| `part` | `{{ part .ref "/" 0 }}` on `"OFF1/A/42/2026"` | `OFF1` — past the end gives `""`, not an error |
| `split` | `{{ range split .codes "," }}` | iterate the fields |
| `join` | `{{ join .attachment_ids ", " }}` | `DOC-0001, DOC-0002` |
| `date` | `{{ date .issued_at "2006-01-02" "02/01/2006" }}` on `"2026-10-01"` | `01/10/2026` — a value that does not match the input layout is an error, never a guess |
| `decimal` | `{{ decimal .fee_amount 2 }}` on `1400` | `1400.00` — exact, see below |
| `lookup` | `{{ lookup .grade "A" "Premium" "B" "Standard" }}` | `Premium` — an unmapped value passes through unchanged |
| `zero` | `{{ if zero .gain }}` | true for nil, `""`, `false`, and numeric zero |
| `coalesce` | `{{ coalesce .valid_until "Not specified" }}` | first value present |
| `trim` | `{{ trim .name }}` | surrounding whitespace removed |

`zero` exists because JSON numbers are carried as `json.Number`, which is a string underneath, so
`{{ if .quantity }}` is true even when the quantity is `0`. Use `{{ if zero .quantity }}`.

`decimal` holds the value exactly rather than as a `float64`: an integer past 2^53 keeps every
digit, and a half rounds away from zero, applied to the digits as written. A magnitude beyond
roughly 1200 digits is refused rather than expanded.

An absent value stays absent throughout. A missing key, a JSON `null` and an empty string all render
as nothing rather than as `0.00` or a zero date — writing a figure the data never carried would be a
silent change of the document. A value that really is `0` still formats as `0.00`.

## Resolvers

Most templates need none: the document's content belongs in the data, and the helpers cover
formatting. Supply a resolver only for a value that cannot reasonably be put in the data, such as a
lookup against a code list:

```go
doc, err := htmlgen.Generate(ctx, tmpl, data, htmlgen.WithResolvers(htmlgen.Resolvers{
    "codelist": func(ctx context.Context, args ...any) (any, error) {
        return store.Describe(ctx, args[0].(string), args[1].(string))
    },
}))
```

```html
<td>{{ codelist "country" .destination }}</td>
```

Each resolver is registered under its own name, so a template calling one that was not supplied
fails when the template is **parsed** — not at execution, if and when a branch happens to reach it.
Arguments arrive as the caller's own Go values (`string`, `json.Number`, `map[string]any`, `nil`),
and the return value is escaped like any other. A name that would shadow a template builtin, a
helper, or one of html/template's internal `_html_template_*` escapers is refused.

> [!IMPORTANT]
> A resolver must not have durable side effects. The template engine calls a function once per
> *evaluated* occurrence and skips occurrences in branches it does not take, so minting a
> document number inside `{{ if .approved }}…{{ end }}` would make the counter depend on the
> shape of the template, and a retried render would advance it again. Mint the number before
> calling `Generate` and put it in the data.

## Validation

`Validate(tmpl, resolverNames...)` checks a template without rendering it: syntax, calls to
functions that were not declared, and html/template's escaping pass, which a plain parse does not
run. Use it when a template is stored or loaded, and in CI over a template directory, so a broken
template fails at deploy rather than when someone requests a document. It cannot detect data
problems; only `Generate` can.

Every failure matches a sentinel with `errors.Is`: `ErrParseTemplate`, `ErrUnsupportedTemplate`,
`ErrUnsafeTemplate`, `ErrInvalidData`, `ErrUnsupportedValue`, `ErrUnsafeValue`, `ErrMissingKey`,
`ErrHelper`, `ErrResolver`, `ErrRender`, `ErrOutputTooLarge`, `ErrInvalidResolverName`,
`ErrReservedResolverName`. A resolver's own error stays reachable, so `errors.Is(err, ErrResolver)`
and `errors.Is(err, yourSentinel)` are both true.

## Options

| Option | Effect |
|---|---|
| `WithResolvers(r)` | caller functions a template may call by name |
| `WithStrictKeys()` | a referenced-but-absent key becomes an error; for tests and CI |
| `WithMaxOutputBytes(n)` | caps the document; default 32 MiB, negative removes the cap |

## Testing

```sh
go test -race ./...
go test ./... -update   # rewrite the golden files after an intended change
```
