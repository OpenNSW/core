// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen_test

import (
	"context"
	"encoding/xml"
	"testing"

	"github.com/OpenNSW/core/xmlgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// render is the shorthand every test here uses: one element around one value.
func render(t *testing.T, body string, data any, opts ...xmlgen.Option) string {
	t.Helper()
	out, err := xmlgen.Generate(context.Background(), []byte("<R>"+body+"</R>"), data, opts...)
	require.NoError(t, err)
	assertNoMarkers(t, out)
	return string(out)
}

// assertNoMarkers guards every successful render against text/template's
// placeholders reaching a document.
func assertNoMarkers(t *testing.T, out []byte) {
	t.Helper()
	for _, marker := range []string{"<no value>", "<nil>", "<invalid reflect.Value>"} {
		assert.NotContains(t, string(out), marker, "template placeholder leaked into the document")
	}
}

// inner unmarshals <R>…</R> back to the text a conforming parser sees, which
// is what "semantic equivalence" means in practice.
func inner(t *testing.T, doc string) string {
	t.Helper()
	var v struct {
		Text string `xml:",chardata"`
	}
	require.NoError(t, xml.Unmarshal([]byte(doc), &v))
	return v.Text
}

func TestEscaping_Metacharacters(t *testing.T) {
	t.Run("ampersand survives as a value", func(t *testing.T) {
		doc := render(t, "{{ .v }}", map[string]any{"v": "Smith & Co Ltd"})
		assert.Contains(t, doc, "Smith &amp; Co Ltd")
		assert.Equal(t, "Smith & Co Ltd", inner(t, doc))
	})

	t.Run("angle brackets are escaped", func(t *testing.T) {
		doc := render(t, "{{ .v }}", map[string]any{"v": "A<B>C"})
		assert.Contains(t, doc, "A&lt;B&gt;C")
		assert.Equal(t, "A<B>C", inner(t, doc))
	})

	t.Run("quotes use numeric references", func(t *testing.T) {
		doc := render(t, "{{ .v }}", map[string]any{"v": `he said "hi" and 'bye'`})
		assert.Contains(t, doc, "&#34;")
		assert.Contains(t, doc, "&#39;")
	})

	t.Run("a value carrying markup cannot forge elements", func(t *testing.T) {
		doc := render(t, "{{ .v }}", map[string]any{"v": "<Consignee><Name>Evil</Name></Consignee>"})

		var probe struct {
			Children []struct{} `xml:",any"`
		}
		require.NoError(t, xml.Unmarshal([]byte(doc), &probe))
		assert.Empty(t, probe.Children, "escaped markup must not become child elements")
		assert.Equal(t, "<Consignee><Name>Evil</Name></Consignee>", inner(t, doc))
	})

	t.Run("an injection closing the current element is neutralised", func(t *testing.T) {
		doc := render(t, "{{ .v }}", map[string]any{"v": "</R><R>other"})
		assert.Equal(t, "</R><R>other", inner(t, doc))
	})

	t.Run("already-escaped input is escaped again rather than sniffed", func(t *testing.T) {
		doc := render(t, "{{ .v }}", map[string]any{"v": "Smith &amp; Co"})
		assert.Contains(t, doc, "&amp;amp;")
		assert.Equal(t, "Smith &amp; Co", inner(t, doc))
	})
}

// TestEscaping_AssociatedTemplates covers {{ define }} and {{ block }}, whose
// bodies text/template installs as separate parse trees. Escaping them takes
// walking every tree a parse produced, not just the main one — walking only
// the main tree let a value carrying markup forge elements, and the output
// stayed well-formed so the document check could not catch it.
func TestEscaping_AssociatedTemplates(t *testing.T) {
	const evil = "<Consignee><Name>Evil</Name></Consignee>"

	tests := []struct {
		name string
		tmpl string
	}{
		{
			name: "block",
			tmpl: `<R>{{block "item" .}}<Item>{{ .v }}</Item>{{end}}</R>`,
		},
		{
			name: "define and template",
			tmpl: `{{define "item"}}<Item>{{ .v }}</Item>{{end}}<R>{{template "item" .}}</R>`,
		},
		{
			// blockControl shares the parent's tree set, so this body is a
			// third tree. A fix that walked only one level of association
			// would miss it.
			name: "block nested in define",
			tmpl: `{{define "outer"}}<Item>{{block "row" .}}{{ .v }}{{end}}</Item>{{end}}<R>{{template "outer" .}}</R>`,
		},
		{
			name: "block body reached through range",
			tmpl: `<R>{{range .rows}}{{block "row" .}}<Item>{{ .v }}</Item>{{end}}{{end}}</R>`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := map[string]any{"v": evil, "rows": []any{map[string]any{"v": evil}}}
			out, err := xmlgen.Generate(context.Background(), []byte(tc.tmpl), data)
			require.NoError(t, err)
			assertNoMarkers(t, out)

			assert.Contains(t, string(out), "&lt;Consignee&gt;")
			assert.NotContains(t, string(out), "<Consignee>", "markup must not reach the document intact")

			var probe struct {
				Item struct {
					Text     string     `xml:",chardata"`
					Children []struct{} `xml:",any"`
				} `xml:"Item"`
			}
			require.NoError(t, xml.Unmarshal(out, &probe))
			assert.Empty(t, probe.Item.Children, "escaped markup must not become child elements")
			assert.Equal(t, evil, probe.Item.Text)
		})
	}
}

func TestEscaping_Context(t *testing.T) {
	t.Run("a value is safe inside a double-quoted attribute", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(),
			[]byte(`<R a="{{ .v }}"/>`), map[string]any{"v": `x"y<z&w`})
		require.NoError(t, err)

		var probe struct {
			A string `xml:"a,attr"`
		}
		require.NoError(t, xml.Unmarshal(out, &probe))
		assert.Equal(t, `x"y<z&w`, probe.A)
	})

	t.Run("a value is safe inside a single-quoted attribute", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(),
			[]byte(`<R a='{{ .v }}'/>`), map[string]any{"v": `x'y<z&w`})
		require.NoError(t, err)

		var probe struct {
			A string `xml:"a,attr"`
		}
		require.NoError(t, xml.Unmarshal(out, &probe))
		assert.Equal(t, `x'y<z&w`, probe.A)
	})

	t.Run("tabs and newlines round-trip through numeric references", func(t *testing.T) {
		const address = "NO: 204,\t\nCANNEL ROAD,\t\nELAKANDA."
		doc := render(t, "{{ .v }}", map[string]any{"v": address})
		assert.Contains(t, doc, "&#x9;")
		assert.Contains(t, doc, "&#xA;")
		assert.Equal(t, address, inner(t, doc), "a multi-line address must survive intact")
	})

	t.Run("map keys and range values are escaped", func(t *testing.T) {
		doc := render(t, `{{ range $k, $v := .m }}<E k="{{ $k }}">{{ $v }}</E>{{ end }}`,
			map[string]any{"m": map[string]any{"a&b": "c&d"}})
		assert.Contains(t, doc, "a&amp;b")
		assert.Contains(t, doc, "c&amp;d")
	})

	t.Run("escaping applies at the end of a pipeline", func(t *testing.T) {
		doc := render(t, `{{ .v | printf "%s!" }}`, map[string]any{"v": "a&b"})
		assert.Contains(t, doc, "a&amp;b!")
	})
}

func TestEscaping_UnicodeAndControl(t *testing.T) {
	t.Run("non-ASCII passes through as UTF-8", func(t *testing.T) {
		const name = "Ünïcødé Tráder ශ්\u200dරී ලංකා 茶"
		doc := render(t, "{{ .v }}", map[string]any{"v": name})
		assert.Equal(t, name, inner(t, doc))
	})

	t.Run("invalid UTF-8 becomes the replacement character", func(t *testing.T) {
		doc := render(t, "{{ .v }}", map[string]any{"v": "a\xffb"})
		assert.Equal(t, "a�b", inner(t, doc))
	})

	t.Run("a codepoint XML forbids becomes the replacement character", func(t *testing.T) {
		doc := render(t, "{{ .v }}", map[string]any{"v": "a\x00b\x0bc"})
		assert.NotContains(t, doc, "\x00")
		assert.Equal(t, "a�b�c", inner(t, doc))
	})
}

func TestEscaping_OptOut(t *testing.T) {
	t.Run("raw emits a fragment unescaped", func(t *testing.T) {
		doc := render(t, "{{ raw .v }}", map[string]any{"v": "<Inner>x</Inner>"})
		assert.Contains(t, doc, "<Inner>x</Inner>")
	})

	t.Run("raw is not a hole in the document check", func(t *testing.T) {
		_, err := xmlgen.Generate(context.Background(),
			[]byte("<R>{{ raw .v }}</R>"), map[string]any{"v": "a & b"})
		require.ErrorIs(t, err, xmlgen.ErrMalformedXML)
	})

	t.Run("cdata wraps and splits an embedded terminator", func(t *testing.T) {
		doc := render(t, "{{ cdata .v }}", map[string]any{"v": "a]]>b"})
		assert.Contains(t, doc, "]]]]><![CDATA[>")
		assert.Equal(t, "a]]>b", inner(t, doc))
	})

	t.Run("a hand-written xml call is not escaped twice", func(t *testing.T) {
		doc := render(t, "{{ xml .v }}", map[string]any{"v": "a&b"})
		assert.Contains(t, doc, "a&amp;b")
		assert.NotContains(t, doc, "&amp;amp;")
	})

	t.Run("literal text in an else branch reaches the document verbatim", func(t *testing.T) {
		doc := render(t, "{{ if .v }}{{ .v }}{{ else }}<null/>{{ end }}", map[string]any{"v": ""})
		assert.Contains(t, doc, "<null/>")
	})

	t.Run("a declaration is not escaped and prints nothing", func(t *testing.T) {
		doc := render(t, `{{ $x := .v }}{{ $x }}`, map[string]any{"v": "a&b"})
		assert.Contains(t, doc, "a&amp;b")
		assert.NotContains(t, doc, "&amp;amp;")
	})

	t.Run("the HTML and JS escapers are refused", func(t *testing.T) {
		for _, fn := range []string{"html", "js", "urlquery"} {
			_, err := xmlgen.Generate(context.Background(),
				[]byte("<R>{{ "+fn+" .v }}</R>"), map[string]any{"v": "a&b"})
			require.ErrorIs(t, err, xmlgen.ErrHelper, "%s should be refused", fn)
		}
	})
}

func TestValues(t *testing.T) {
	t.Run("a large number keeps its digits", func(t *testing.T) {
		doc := render(t, "{{ .v }}", []byte(`{"v":10000000}`))
		assert.Contains(t, doc, ">10000000<")
	})

	t.Run("a trailing zero survives JSON input", func(t *testing.T) {
		doc := render(t, "{{ .v }}", []byte(`{"v":1.50}`))
		assert.Contains(t, doc, ">1.50<")
	})

	t.Run("an integer beyond float precision stays exact", func(t *testing.T) {
		doc := render(t, "{{ .v }}", []byte(`{"v":9223372036854775807}`))
		assert.Contains(t, doc, ">9223372036854775807<")
	})

	t.Run("a pre-decoded float avoids exponent notation", func(t *testing.T) {
		doc := render(t, "{{ .v }}", map[string]any{"v": float64(1e7)})
		assert.Contains(t, doc, ">10000000<")
	})

	t.Run("a missing key and a null both render empty", func(t *testing.T) {
		doc := render(t, "<A>{{ .missing }}</A><B>{{ .null }}</B>", []byte(`{"null":null}`))
		assert.Contains(t, doc, "<A></A>")
		assert.Contains(t, doc, "<B></B>")
	})

	t.Run("a pointer cycle is refused rather than crashing the process", func(t *testing.T) {
		// A Go stack overflow is fatal and recover cannot catch it, so an
		// unbounded walk here would take the process down rather than fail
		// the render. JSON cannot express a cycle, but a caller passing
		// native Go values can.
		type loop *any
		var cycle any
		inner := loop(&cycle)
		cycle = inner

		require.NotPanics(t, func() {
			_, err := xmlgen.Generate(context.Background(),
				[]byte("<R>{{ .v }}</R>"), map[string]any{"v": cycle})
			require.ErrorIs(t, err, xmlgen.ErrUnsupportedValue)
		})
	})

	t.Run("a map or slice is refused rather than printed as Go syntax", func(t *testing.T) {
		for name, v := range map[string]any{
			"map":   map[string]any{"a": 1},
			"slice": []any{1, 2},
			"bytes": []byte("x"),
		} {
			_, err := xmlgen.Generate(context.Background(),
				[]byte("<R>{{ .v }}</R>"), map[string]any{"v": v})
			require.ErrorIs(t, err, xmlgen.ErrUnsupportedValue, "%s should be refused", name)
		}
	})

	t.Run("scalars of every kind render", func(t *testing.T) {
		doc := render(t, "{{ .b }}|{{ .i }}|{{ .u }}|{{ .f }}",
			map[string]any{"b": true, "i": int64(-7), "u": uint8(3), "f": float32(1.5)})
		assert.Equal(t, "true|-7|3|1.5", inner(t, doc))
	})
}
