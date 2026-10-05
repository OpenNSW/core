// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package htmlgen renders print-ready HTML documents from Go html/template
// skeletons and structured data.
//
// A template is the target HTML with {{ }} actions in it:
//
//	<h1>Permit</h1>
//	<p>Permit No: {{ .permit_number }}</p>
//	{{- range .items }}
//	<li>{{ .code }}</li>
//	{{- end }}
//
// It is the HTML sibling of xmlgen and follows the same shape: bytes in,
// bytes out, the same helpers, and the same rules for values. The caller
// assembles everything the document needs into data before calling Generate;
// the template only arranges and formats it.
//
// # Escaping
//
// Escaping is html/template's contextual escaping, unchanged: a value is
// escaped for the place it is printed — element text, a quoted attribute, a
// URL, a script or a style block. A value carrying markup cannot forge
// elements, and a javascript: URL cannot reach an href.
//
// When html/template refuses a value for its context it prints the marker
// ZgotmplZ in its place. htmlgen does not return such a document: it reports
// ErrUnsafeValue instead. A template whose contexts html/template cannot
// determine is reported as ErrUnsafeTemplate.
//
// A caller that needs to print trusted markup or a data: URL passes it as one
// of html/template's typed strings — template.HTML, template.URL and the
// rest — from Go code. JSON data cannot produce those types, so nothing a
// user submitted can opt itself out of escaping.
//
// # Values
//
// Before html/template escapes a value, htmlgen converts it to text by the
// same rules as xmlgen. A key the template references but the data does not
// have renders as the empty string, as does a JSON null. WithStrictKeys turns
// the first into an error, which is worth enabling in tests over a template.
// A map or slice has no text form and is an error rather than Go syntax in
// the document.
//
// Numbers keep their exact text when data is supplied as JSON bytes, which are
// decoded with json.Decoder.UseNumber: 10000000 stays 10000000 rather than
// becoming 1e+07, and 1.50 keeps its trailing zero.
//
// # Output
//
// Output is bytes: an HTML document a browser can display and print, or a
// converter can turn into PDF. htmlgen does not produce PDF itself.
package htmlgen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"strings"
)

// defaultMaxOutputBytes caps a render that would otherwise grow without bound,
// for instance from a range over unexpectedly large data.
const defaultMaxOutputBytes int64 = 32 << 20 // 32 MiB

// unsafeMarker is what html/template prints in place of a value it refused
// for its context.
const unsafeMarker = "ZgotmplZ"

type options struct {
	resolvers      Resolvers
	strictKeys     bool
	maxOutputBytes int64
}

// Option configures a render.
type Option func(*options)

// WithResolvers supplies caller functions a template may call by name. They
// are for values that cannot be computed from the data — a lookup against a
// code list or another service. Formatting is already covered by the built-in
// helpers, and the document's own data belongs in data, so most templates
// need none.
func WithResolvers(r Resolvers) Option {
	return func(o *options) { o.resolvers = r }
}

// WithStrictKeys makes a key the template references but the data does not
// contain an error rather than an empty value.
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

// Generate renders tmpl against data and returns the HTML document.
//
// data may be []byte or json.RawMessage, decoded internally so numeric
// literals keep their exact text, or any value html/template can walk: a
// map[string]any, a slice, or a struct addressed by Go field name.
//
// Generate is safe for concurrent use, provided any resolvers are.
func Generate(ctx context.Context, tmpl []byte, data any, opts ...Option) ([]byte, error) {
	return generate(ctx, tmpl, data, newOptions(opts))
}

// GenerateTo is Generate writing to w.
//
// It does not stream. It renders and checks in full and then writes once, so
// w receives nothing at all unless the whole document rendered — a caller
// writing to an http.ResponseWriter cannot emit half a document after a 200.
func GenerateTo(ctx context.Context, w io.Writer, tmpl []byte, data any, opts ...Option) error {
	doc, err := Generate(ctx, tmpl, data, opts...)
	if err != nil {
		return err
	}
	_, err = w.Write(doc)
	return err
}

// Validate reports whether tmpl is a usable htmlgen template, without
// rendering it. resolverNames are the names it is allowed to call beyond the
// built-in helpers; a call to anything else is reported here, as is a
// template html/template cannot escape safely.
//
// It is for load-time checks — validating a template as it is stored, or a CI
// sweep over a template directory — so a broken template fails at deploy
// rather than when someone requests a document.
//
// It does not check the arguments passed to helpers or resolvers — a date
// that does not match its layout, say — even when they are written into the
// template as literals. Those, like data problems, only Generate can find.
func Validate(tmpl []byte, resolverNames ...string) error {
	resolvers := make(Resolvers, len(resolverNames))
	for _, n := range resolverNames {
		resolvers[n] = func(context.Context, ...any) (any, error) { return nil, nil }
	}
	t, err := compile(context.Background(), tmpl, options{resolvers: resolvers})
	if err != nil {
		return err
	}
	// html/template works out each action's context lazily, on the first
	// Execute, so that is the only way to surface an escaping error. The
	// escaping pass covers the whole template before execution starts, and
	// the writer refuses the first byte, so execution stops there.
	err = t.Execute(refuseWriter{}, nil)
	if escapeErr, ok := asEscapeError(err); ok {
		return fmt.Errorf("%w: %w", ErrUnsafeTemplate, escapeErr)
	}
	return nil
}

// compile parses a template and rewrites it to apply htmlgen's value rules.
func compile(ctx context.Context, tmpl []byte, opts options) (*template.Template, error) {
	funcs, err := buildFuncMap(ctx, opts.resolvers)
	if err != nil {
		return nil, err
	}

	// Funcs must precede Parse: only then does a call to a function that was
	// never supplied fail at parse time rather than at execution, on whichever
	// branch happens to reach it.
	t := template.New("htmlgen").Funcs(funcs)
	if opts.strictKeys {
		t = t.Option("missingkey=error")
	}
	t, err = t.Parse(string(stripBOM(tmpl)))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParseTemplate, err)
	}
	// The whole template, not t.Tree: a {{ define }} or {{ block }} body is a
	// separate tree and would otherwise skip the value rules.
	if err := injectText(t); err != nil {
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
	if bytes.Contains(doc, []byte(unsafeMarker)) {
		return nil, fmt.Errorf("%w: html/template printed %s in place of a value", ErrUnsafeValue, unsafeMarker)
	}
	return doc, nil
}

// executionError turns html/template's wrapping back into the cause a caller
// can act on.
func executionError(w *limitWriter, err error) error {
	// A writer failure surfaces through Execute as an opaque write error; the
	// real reason was recorded when the write was refused.
	if w.err != nil {
		return w.err
	}
	if escapeErr, ok := asEscapeError(err); ok {
		return fmt.Errorf("%w: %w", ErrUnsafeTemplate, escapeErr)
	}
	if re, ok := asResolverError(err); ok {
		return fmt.Errorf("%w: %s: %w", ErrResolver, re.name, re.err)
	}
	if looksLikeMissingKey(err) {
		return fmt.Errorf("%w: %w", ErrMissingKey, err)
	}
	return fmt.Errorf("%w: %w", ErrRender, err)
}

// asEscapeError recovers the error html/template reports when it cannot
// escape a template, as distinct from a failure while executing it.
func asEscapeError(err error) (*template.Error, bool) {
	var e *template.Error
	ok := errors.As(err, &e)
	return e, ok
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
	// Decode stops after the first value; anything but whitespace after it
	// would otherwise be dropped without an error.
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: unexpected content after the JSON value", ErrInvalidData)
	}
	return v, nil
}

// stripBOM removes a leading UTF-8 byte order mark. One survives editing a
// template on Windows and would otherwise sit in front of the doctype.
func stripBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
}

// looksLikeMissingKey reports whether an execution error came from
// missingkey=error, which the template engine reports only as a message.
func looksLikeMissingKey(err error) bool {
	s := err.Error()
	return strings.Contains(s, "map has no entry for key") || strings.Contains(s, "nil data; no entry for key")
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

// refuseWriter fails every write. Validate executes against it to trigger
// html/template's escaping pass without rendering anything.
type refuseWriter struct{}

var errRefused = errors.New("htmlgen: validation does not render")

func (refuseWriter) Write([]byte) (int, error) { return 0, errRefused }
