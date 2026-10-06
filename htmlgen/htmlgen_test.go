// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package htmlgen_test

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"strings"
	"sync"
	"testing"

	"github.com/OpenNSW/core/htmlgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// render generates tmpl against data and returns the document as a string,
// failing the test on any error.
func render(t *testing.T, tmpl string, data any, opts ...htmlgen.Option) string {
	t.Helper()
	out, err := htmlgen.Generate(context.Background(), []byte(tmpl), data, opts...)
	require.NoError(t, err)
	return string(out)
}

func TestGenerate_DataShapes(t *testing.T) {
	const tmpl = `<p>{{ .name }}</p>`

	t.Run("JSON bytes and json.RawMessage both decode", func(t *testing.T) {
		raw := []byte(`{"name":"Acme"}`)
		a := render(t, tmpl, raw)
		b := render(t, tmpl, json.RawMessage(raw))
		assert.Equal(t, a, b)
		assert.Equal(t, "<p>Acme</p>", a)
	})

	t.Run("malformed JSON is reported as bad data", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(), []byte(tmpl), []byte(`{"name":`))
		require.ErrorIs(t, err, htmlgen.ErrInvalidData)
	})

	t.Run("a map passes through", func(t *testing.T) {
		assert.Equal(t, "<p>Acme</p>", render(t, tmpl, map[string]any{"name": "Acme"}))
	})

	t.Run("a struct is addressed by Go field name", func(t *testing.T) {
		data := struct {
			Name string `json:"name"`
		}{Name: "Acme"}

		assert.Equal(t, "<p>Acme</p>", render(t, `<p>{{ .Name }}</p>`, data))

		// The json tag is not consulted: the template engine walks Go fields.
		_, err := htmlgen.Generate(context.Background(), []byte(tmpl), data)
		require.Error(t, err)
	})

	t.Run("nil data renders a template that needs none", func(t *testing.T) {
		assert.Equal(t, "<p>static</p>", render(t, `<p>static</p>`, nil))
	})

	t.Run("a top-level array can be ranged over", func(t *testing.T) {
		out := render(t, `{{ range . }}<li>{{ .k }}</li>{{ end }}`, []byte(`[{"k":"a"},{"k":"b"}]`))
		assert.Equal(t, "<li>a</li><li>b</li>", out)
	})

	t.Run("a leading byte order mark is dropped from template and data", func(t *testing.T) {
		out := render(t, "\xef\xbb\xbf<!DOCTYPE html><p>{{ .name }}</p>", []byte("\xef\xbb\xbf{\"name\":\"Acme\"}"))
		assert.Equal(t, "<!DOCTYPE html><p>Acme</p>", out)
	})
}

func TestGenerate_MissingKeys(t *testing.T) {
	t.Run("an absent key renders empty by default", func(t *testing.T) {
		assert.Equal(t, "<p></p>", render(t, `<p>{{ .nope }}</p>`, map[string]any{}))
	})

	t.Run("a path through an absent key renders empty", func(t *testing.T) {
		assert.Equal(t, "<p></p>", render(t, `<p>{{ .a.b.c }}</p>`, []byte(`{}`)))
	})

	t.Run("a JSON null renders empty", func(t *testing.T) {
		assert.Equal(t, "<p></p>", render(t, `<p>{{ .v }}</p>`, []byte(`{"v":null}`)))
	})

	t.Run("an absent key is falsy in a condition", func(t *testing.T) {
		assert.Equal(t, "<p>no</p>", render(t, `<p>{{ if .nope }}yes{{ else }}no{{ end }}</p>`, map[string]any{}))
	})

	t.Run("WithStrictKeys turns an absent key into an error", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(), []byte(`<p>{{ .nope }}</p>`),
			map[string]any{}, htmlgen.WithStrictKeys())
		require.ErrorIs(t, err, htmlgen.ErrMissingKey)
	})

	t.Run("WithStrictKeys accepts a key that is present", func(t *testing.T) {
		out := render(t, `<p>{{ .v }}</p>`, map[string]any{"v": "x"}, htmlgen.WithStrictKeys())
		assert.Equal(t, "<p>x</p>", out)
	})
}

func TestGenerate_Escaping(t *testing.T) {
	t.Run("markup in element text is escaped", func(t *testing.T) {
		out := render(t, `<p>{{ .v }}</p>`, map[string]any{"v": `<script>alert("x")</script> & co`})
		assert.Equal(t, "<p>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; co</p>", out)
	})

	t.Run("a quote cannot break out of an attribute", func(t *testing.T) {
		out := render(t, `<td title="{{ .v }}">x</td>`, map[string]any{"v": `" onmouseover="alert(1)`})
		assert.NotContains(t, out, `" onmouseover="`)
		assert.Contains(t, out, `&#34; onmouseover=&#34;alert(1)`)
	})

	t.Run("a value in a script block is encoded as a JavaScript value", func(t *testing.T) {
		out := render(t, `<script>var ref = {{ .v }};</script>`, map[string]any{"v": "</script><b>"})
		assert.NotContains(t, out, "</script><b>")
		assert.Contains(t, out, "u003c/script", "the closing tag is encoded as a JavaScript escape")
	})

	t.Run("a javascript: URL is refused rather than rendered as a marker", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(), []byte(`<a href="{{ .u }}">x</a>`),
			map[string]any{"u": "javascript:alert(1)"})
		require.ErrorIs(t, err, htmlgen.ErrUnsafeValue)
	})

	t.Run("a data: URL from data is refused in an img src", func(t *testing.T) {
		// A string from the data is untrusted, so html/template will not let
		// it pick a scheme other than http, https or mailto.
		_, err := htmlgen.Generate(context.Background(), []byte(`<img src="{{ .img }}">`),
			[]byte(`{"img":"data:image/png;base64,AAAA"}`))
		require.ErrorIs(t, err, htmlgen.ErrUnsafeValue)
	})

	t.Run("an ordinary https URL is allowed in an href", func(t *testing.T) {
		out := render(t, `<a href="{{ .u }}">x</a>`, map[string]any{"u": "https://example.org/a?b=c&d=e"})
		assert.Equal(t, `<a href="https://example.org/a?b=c&amp;d=e">x</a>`, out)
	})

	t.Run("bodies of define and block are escaped too", func(t *testing.T) {
		out := render(t,
			`{{ define "x" }}<i>{{ .a }}</i>{{ end }}{{ template "x" . }}{{ block "y" . }}<u>{{ .a }}</u>{{ end }}`,
			map[string]any{"a": "<&>"})
		assert.Equal(t, "<i>&lt;&amp;&gt;</i><u>&lt;&amp;&gt;</u>", out)
	})

	t.Run("a range variable is escaped", func(t *testing.T) {
		out := render(t, `{{ range $k, $v := . }}<li>{{ $k }}={{ $v }}</li>{{ end }}`,
			map[string]any{"<k>": "<v>"})
		assert.Equal(t, "<li>&lt;k&gt;=&lt;v&gt;</li>", out)
	})

	t.Run("text outside an action is emitted verbatim", func(t *testing.T) {
		out := render(t, `<p>{{ if .v }}{{ .v }}{{ else }}<em>not declared</em>{{ end }}</p>`, map[string]any{})
		assert.Equal(t, "<p><em>not declared</em></p>", out)
	})
}

func TestGenerate_TrustedValues(t *testing.T) {
	// The typed strings are how Go code opts a value out of escaping. They
	// cannot come from JSON, so user-submitted data never reaches this path.
	t.Run("template.HTML from Go code is emitted as markup", func(t *testing.T) {
		out := render(t, `<div>{{ .sig }}</div>`, map[string]any{"sig": template.HTML("<b>Signed</b>")})
		assert.Equal(t, "<div><b>Signed</b></div>", out)
	})

	t.Run("template.URL from Go code may carry a data: URL", func(t *testing.T) {
		out := render(t, `<img src="{{ .img }}">`,
			map[string]any{"img": template.URL("data:image/png;base64,AAAA")})
		assert.Equal(t, `<img src="data:image/png;base64,AAAA">`, out)
	})

	t.Run("the same markup from JSON is escaped", func(t *testing.T) {
		out := render(t, `<div>{{ .sig }}</div>`, []byte(`{"sig":"<b>Signed</b>"}`))
		assert.Equal(t, "<div>&lt;b&gt;Signed&lt;/b&gt;</div>", out)
	})
}

func TestGenerate_Values(t *testing.T) {
	t.Run("JSON numbers keep their exact text", func(t *testing.T) {
		out := render(t, `{{ .a }}|{{ .b }}|{{ .c }}`, []byte(`{"a":10000000,"b":1.50,"c":9007199254740993}`))
		assert.Equal(t, "10000000|1.50|9007199254740993", out)
	})

	t.Run("a float64 never prints with an exponent", func(t *testing.T) {
		assert.Equal(t, "10000000", render(t, `{{ .v }}`, map[string]any{"v": float64(1e7)}))
	})

	t.Run("booleans print as true and false", func(t *testing.T) {
		assert.Equal(t, "true|false", render(t, `{{ .a }}|{{ .b }}`, []byte(`{"a":true,"b":false}`)))
	})

	t.Run("a named string type prints its value", func(t *testing.T) {
		type grade string
		assert.Equal(t, "A", render(t, `{{ .g }}`, map[string]any{"g": grade("A")}))
	})

	t.Run("a nil pointer prints empty", func(t *testing.T) {
		var p *string
		assert.Equal(t, "[]", render(t, `[{{ .p }}]`, map[string]any{"p": p}))
	})

	t.Run("a map is an error rather than Go syntax", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(), []byte(`<p>{{ .m }}</p>`), []byte(`{"m":{"k":1}}`))
		require.ErrorIs(t, err, htmlgen.ErrUnsupportedValue)
	})

	t.Run("a slice is an error rather than Go syntax", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(), []byte(`<p>{{ .s }}</p>`), []byte(`{"s":[1,2]}`))
		require.ErrorIs(t, err, htmlgen.ErrUnsupportedValue)
	})

	t.Run("a []byte is an error", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(), []byte(`<p>{{ .b }}</p>`), map[string]any{"b": []byte("x")})
		require.ErrorIs(t, err, htmlgen.ErrUnsupportedValue)
	})

	t.Run("a pointer cycle fails the render instead of the process", func(t *testing.T) {
		var a any
		a = &a
		_, err := htmlgen.Generate(context.Background(), []byte(`<p>{{ .v }}</p>`), map[string]any{"v": a})
		require.ErrorIs(t, err, htmlgen.ErrUnsupportedValue)
	})
}

func TestGenerate_Failures(t *testing.T) {
	t.Run("a syntax error is a parse error", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(), []byte(`<p>{{ .name </p>`), nil)
		require.ErrorIs(t, err, htmlgen.ErrParseTemplate)
	})

	t.Run("a call to an undefined template is a parse error", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(), []byte(`<p>{{ template "nosuch" }}</p>`), nil)
		require.ErrorIs(t, err, htmlgen.ErrParseTemplate)
		require.NotErrorIs(t, err, htmlgen.ErrUnsafeTemplate)
		assert.Contains(t, err.Error(), "no such template")
	})

	t.Run("an ambiguous context is an unsafe template", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(),
			[]byte(`{{ if .x }}<a href="{{ else }}<b>{{ end }}`), map[string]any{})
		require.ErrorIs(t, err, htmlgen.ErrUnsafeTemplate)
	})

	t.Run("output past the cap is abandoned", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(),
			[]byte(`{{ range . }}<li>{{ . }}</li>{{ end }}`), []byte(`["aaaaaaaaaa","bbbbbbbbbb"]`),
			htmlgen.WithMaxOutputBytes(16))
		require.ErrorIs(t, err, htmlgen.ErrOutputTooLarge)
	})

	t.Run("a negative cap removes the limit", func(t *testing.T) {
		out := render(t, `<p>{{ .v }}</p>`, map[string]any{"v": strings.Repeat("x", 64)},
			htmlgen.WithMaxOutputBytes(-1))
		assert.Len(t, out, 71)
	})

	t.Run("a cancelled context stops the render", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := htmlgen.Generate(ctx, []byte(`<p>x</p>`), nil)
		require.ErrorIs(t, err, htmlgen.ErrRender)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestGenerateTo(t *testing.T) {
	t.Run("writes the whole document", func(t *testing.T) {
		var buf bytes.Buffer
		err := htmlgen.GenerateTo(context.Background(), &buf, []byte(`<p>{{ .v }}</p>`), map[string]any{"v": "x"})
		require.NoError(t, err)
		assert.Equal(t, "<p>x</p>", buf.String())
	})

	t.Run("writes nothing when the render fails part-way", func(t *testing.T) {
		var buf bytes.Buffer
		err := htmlgen.GenerateTo(context.Background(), &buf,
			[]byte(`<p>before</p><a href="{{ .u }}">x</a>`), map[string]any{"u": "javascript:alert(1)"})
		require.ErrorIs(t, err, htmlgen.ErrUnsafeValue)
		assert.Zero(t, buf.Len(), "a failed render must not leave half a document behind")
	})
}

func TestValidate(t *testing.T) {
	t.Run("accepts a usable template", func(t *testing.T) {
		require.NoError(t, htmlgen.Validate([]byte(`<p>{{ date .d "2006-01-02" "02/01/2006" }}</p>`)))
	})

	t.Run("reports a syntax error", func(t *testing.T) {
		require.ErrorIs(t, htmlgen.Validate([]byte(`<p>{{ .name </p>`)), htmlgen.ErrParseTemplate)
	})

	t.Run("reports a call to a function that was not declared", func(t *testing.T) {
		err := htmlgen.Validate([]byte(`<p>{{ if .x }}{{ codelist .y }}{{ end }}</p>`))
		require.ErrorIs(t, err, htmlgen.ErrParseTemplate)
		assert.Contains(t, err.Error(), "not defined")
	})

	t.Run("reports a call to a template that was not defined", func(t *testing.T) {
		// Like a missing function, but html/template only resolves template
		// calls in its escaping pass, so this also covers a branch not taken.
		err := htmlgen.Validate([]byte(`<p>{{ if .x }}{{ template "nosuch" }}{{ end }}</p>`))
		require.ErrorIs(t, err, htmlgen.ErrParseTemplate)
		require.NotErrorIs(t, err, htmlgen.ErrUnsafeTemplate)
		assert.Contains(t, err.Error(), "no such template")
	})

	t.Run("accepts a call to a declared resolver", func(t *testing.T) {
		require.NoError(t, htmlgen.Validate([]byte(`<p>{{ codelist .y }}</p>`), "codelist"))
	})

	t.Run("reports a template html/template cannot escape", func(t *testing.T) {
		// The context error only surfaces once html/template's escaping pass
		// runs, which a plain parse does not trigger.
		err := htmlgen.Validate([]byte(`<a href="{{ .u }}>`))
		require.ErrorIs(t, err, htmlgen.ErrUnsafeTemplate)
	})

	t.Run("does not mistake data problems for template problems", func(t *testing.T) {
		require.NoError(t, htmlgen.Validate([]byte(`<p>{{ .a.b.c }}{{ range .items }}{{ .x }}{{ end }}</p>`)))
	})
}

func TestGenerate_Concurrent(t *testing.T) {
	tmpl := []byte(`<p>{{ .n }}</p>`)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			out, err := htmlgen.Generate(context.Background(), tmpl, map[string]any{"n": i})
			assert.NoError(t, err)
			assert.Contains(t, string(out), "<p>")
		})
	}
	wg.Wait()
}

func TestGenerate_RedundantEscapers(t *testing.T) {
	// html/template already escapes every value for its context. Applying
	// html, js or urlquery as well would escape twice, so each is refused
	// where it is written, in every position a value can come from.
	for _, tmpl := range []string{
		`<p>{{ .x | html }}</p>`,
		`<p>{{ html .x }}</p>`,
		`<script>var a = {{ .x | js }};</script>`,
		`<a href="/q?x={{ .x | urlquery }}">a</a>`,
		`<p>{{ printf "%s" (js .x) }}</p>`,
		`<p>{{ $v := js .x }}{{ $v }}</p>`,
		`<p>{{ with js .x }}{{ . }}{{ end }}</p>`,
		`{{ define "t" }}<p>{{ . }}</p>{{ end }}{{ template "t" (html .x) }}`,
	} {
		t.Run(tmpl, func(t *testing.T) {
			err := htmlgen.Validate([]byte(tmpl))
			require.ErrorIs(t, err, htmlgen.ErrUnsupportedTemplate)
			assert.Contains(t, err.Error(), "is not needed")

			_, err = htmlgen.Generate(context.Background(), []byte(tmpl), map[string]any{"x": "a&b"})
			require.ErrorIs(t, err, htmlgen.ErrUnsupportedTemplate)
		})
	}

	t.Run("a resolver or data key that merely contains the name is fine", func(t *testing.T) {
		out := render(t, `<p>{{ .html }}{{ .js }}</p>`, map[string]any{"html": "a", "js": "b"})
		assert.Equal(t, "<p>ab</p>", out)
	})
}
