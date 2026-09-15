// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package xmlgen renders XML documents from Go text/template skeletons and
// structured data.
//
// A template is the target XML with {{ }} actions in it:
//
//	<Declaration>
//	  <Ref>{{ .refNo }}</Ref>
//	  {{- range .items }}
//	  <Item><Code>{{ .code }}</Code></Item>
//	  {{- end }}
//	</Declaration>
//
// # Escaping
//
// Every value a template prints is XML-escaped automatically: xmlgen rewrites
// the parsed template so each printing action is piped through the xml
// function. A template author cannot forget, and a value carrying markup
// cannot forge elements. Opt out per action with raw or cdata.
//
// Escaping uses encoding/xml.EscapeText, which is correct in element content
// and in attribute values alike. It renders a tab or newline as &#x9; or
// &#xA;, which any conforming parser reads back as the original character.
//
// Text outside an action is emitted verbatim, so a literal empty-value marker
// written in a branch reaches the document unchanged:
//
//	<Gain>{{ if .gain }}{{ .gain }}{{ else }}<null/>{{ end }}</Gain>
//
// # Values
//
// A key the template references but the data does not have renders as the
// empty string, as does a JSON null. WithStrictKeys turns the first into an
// error, which is worth enabling in tests over a template. A map or slice has
// no XML text form and is an error rather than Go syntax in the document.
//
// Numbers keep their exact text when data is supplied as JSON bytes, which are
// decoded with json.Decoder.UseNumber: 10000000 stays 10000000 rather than
// becoming 1e+07, and 1.50 keeps its trailing zero.
//
// # Output
//
// Output is bytes. Before they are returned they are checked as a document —
// exactly one root element, no DOCTYPE, no text outside the root, no
// undeclared namespace prefix — and never rewritten, so the result is
// byte-stable and safe to sign.
//
// Do not reach for html/template as a substitute: its escaping is
// HTML-specific and corrupts XML.
package xmlgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/template"
)

// defaultMaxOutputBytes caps a render that would otherwise grow without bound,
// for instance from a range over unexpectedly large data.
const defaultMaxOutputBytes int64 = 32 << 20 // 32 MiB

type options struct {
	resolvers          Resolvers
	strictKeys         bool
	maxOutputBytes     int64
	skipNamespaceCheck bool
}

// Option configures a render.
type Option func(*options)

// WithResolvers supplies caller functions a template may call by name. They
// are for values that cannot be computed from the data — a lookup against a
// code list or another service. Formatting is already covered by the built-in
// helpers, so most templates need none.
func WithResolvers(r Resolvers) Option {
	return func(o *options) { o.resolvers = r }
}

// WithStrictKeys makes a key the template references but the data does not
// contain an error rather than an empty element.
//
// It is meant for tests over a template and for CI, not for production
// renders: it fires inside {{ if }} and {{ range }} as well, so a genuinely
// optional field needs a guard such as {{ if index . "middle_name" }}.
func WithStrictKeys() Option {
	return func(o *options) { o.strictKeys = true }
}

// WithMaxOutputBytes caps the rendered document. A render that exceeds the cap
// is abandoned part-way with ErrOutputTooLarge. Zero selects the default of
// 32 MiB; a negative value removes the cap.
func WithMaxOutputBytes(n int64) Option {
	return func(o *options) { o.maxOutputBytes = n }
}

// SkipNamespaceCheck disables the undeclared-prefix check. Use it only for a
// document whose namespace declarations live outside this template.
func SkipNamespaceCheck() Option {
	return func(o *options) { o.skipNamespaceCheck = true }
}

func newOptions(opts []Option) options {
	o := options{maxOutputBytes: defaultMaxOutputBytes}
	for _, fn := range opts {
		fn(&o)
	}
	if o.maxOutputBytes == 0 {
		o.maxOutputBytes = defaultMaxOutputBytes
	}
	return o
}

// Generate renders tmpl against data and returns the XML document.
//
// data may be []byte or json.RawMessage, decoded internally so numeric
// literals keep their exact text, or any value text/template can walk: a
// map[string]any, a slice, or a struct addressed by Go field name.
//
// Generate is safe for concurrent use, provided any resolvers are.
func Generate(ctx context.Context, tmpl []byte, data any, opts ...Option) ([]byte, error) {
	return generate(ctx, tmpl, data, newOptions(opts))
}

// GenerateTo is Generate writing to w.
//
// It does not stream: checking a document requires all of it. It renders and
// validates in full and then writes once, so w receives nothing at all unless
// the whole document is valid — a caller writing to an http.ResponseWriter
// cannot emit half a document after a 200.
func GenerateTo(ctx context.Context, w io.Writer, tmpl []byte, data any, opts ...Option) error {
	doc, err := Generate(ctx, tmpl, data, opts...)
	if err != nil {
		return err
	}
	_, err = w.Write(doc)
	return err
}

// Validate reports whether tmpl is a usable xmlgen template, without
// rendering it. resolverNames are the names it is allowed to call beyond the
// built-in helpers; a call to anything else is reported here.
//
// It is for load-time checks — validating a template as it is stored, or a CI
// sweep over a template directory — so a broken template fails at deploy
// rather than when someone submits. It cannot detect data problems; only
// Generate can.
func Validate(tmpl []byte, resolverNames ...string) error {
	resolvers := make(Resolvers, len(resolverNames))
	for _, n := range resolverNames {
		resolvers[n] = func(context.Context, ...any) (any, error) { return nil, nil }
	}
	_, err := compile(context.Background(), tmpl, options{resolvers: resolvers})
	return err
}

// compile parses a template and rewrites it for escaping.
func compile(ctx context.Context, tmpl []byte, opts options) (*template.Template, error) {
	funcs, err := buildFuncMap(ctx, opts.resolvers)
	if err != nil {
		return nil, err
	}

	// Funcs must precede Parse: only then does a call to a function that was
	// never supplied fail at parse time rather than at execution, on whichever
	// branch happens to reach it.
	t := template.New("xmlgen").Funcs(funcs)
	if opts.strictKeys {
		t = t.Option("missingkey=error")
	}
	t, err = t.Parse(string(stripBOM(tmpl)))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParseTemplate, err)
	}
	// The whole template, not t.Tree: a {{ define }} or {{ block }} body is a
	// separate tree and would otherwise render unescaped.
	if err := injectEscaping(t); err != nil {
		return nil, err
	}
	return t, nil
}

func generate(ctx context.Context, tmpl []byte, data any, opts options) ([]byte, error) {
	t, err := compile(ctx, tmpl, opts)
	if err != nil {
		return nil, err
	}

	prepared, err := prepareData(data)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	w := &limitWriter{w: &buf, max: opts.maxOutputBytes, ctx: ctx}
	if err := t.Execute(w, prepared); err != nil {
		return nil, executionError(w, err)
	}

	doc := buf.Bytes()
	if err := checkDocument(doc, opts); err != nil {
		return nil, err
	}
	return doc, nil
}

// executionError turns text/template's wrapping back into the cause a caller
// can act on.
func executionError(w *limitWriter, err error) error {
	// A writer failure surfaces through Execute as an opaque write error; the
	// real reason was recorded when the write was refused.
	if w.err != nil {
		return w.err
	}
	if re, ok := asResolverError(err); ok {
		return fmt.Errorf("%w: %s: %w", ErrResolver, re.name, re.err)
	}
	if looksLikeMissingKey(err) {
		return fmt.Errorf("%w: %w", ErrMissingKey, err)
	}
	return fmt.Errorf("%w: %w", ErrRender, err)
}

// prepareData decodes JSON input and passes anything else through untouched.
func prepareData(data any) (any, error) {
	switch d := data.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return decodeJSON(d)
	case []byte:
		return decodeJSON(d)
	default:
		return data, nil
	}
}

func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(stripBOM(b)))
	// Numbers keep their original text, so a quantity does not acquire an
	// exponent and a monetary value does not lose a trailing zero.
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	return v, nil
}

// limitWriter enforces the size cap and observes context cancellation.
// Execute offers no cancellation hook of its own, so the writer is the only
// place a long render can be stopped.
type limitWriter struct {
	w   io.Writer
	max int64
	n   int64
	ctx context.Context
	err error
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if err := l.ctx.Err(); err != nil {
		l.err = fmt.Errorf("%w: %w", ErrRender, err)
		return 0, l.err
	}
	l.n += int64(len(p))
	if l.max > 0 && l.n > l.max {
		l.err = fmt.Errorf("%w: limit is %d bytes", ErrOutputTooLarge, l.max)
		return 0, l.err
	}
	return l.w.Write(p)
}
