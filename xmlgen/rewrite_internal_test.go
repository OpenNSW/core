// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInjectEscaping_RewritesEveryPrintingAction reads the rewritten tree back
// as source, which is the clearest statement of what the rewriter does.
func TestInjectEscaping_RewritesEveryPrintingAction(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"a plain field", `{{ .a }}`, `{{.a | xml}}`},
		{"a pipeline keeps its stages", `{{ .a | printf "%s" }}`, `{{.a | printf "%s" | xml}}`},
		{"a declaration is left alone", `{{ $x := .a }}`, `{{$x := .a}}`},
		{"a branch condition is left alone", `{{ if .a }}{{ .b }}{{ end }}`, `{{if .a}}{{.b | xml}}{{end}}`},
		{"an else body is rewritten", `{{ if .a }}{{ .b }}{{ else }}{{ .c }}{{ end }}`, `{{if .a}}{{.b | xml}}{{else}}{{.c | xml}}{{end}}`},
		{"a range body is rewritten", `{{ range .a }}{{ . }}{{ end }}`, `{{range .a}}{{. | xml}}{{end}}`},
		{"a with body is rewritten", `{{ with .a }}{{ . }}{{ end }}`, `{{with .a}}{{. | xml}}{{end}}`},
		{"raw suppresses injection", `{{ raw .a }}`, `{{raw .a}}`},
		{"cdata suppresses injection", `{{ cdata .a }}`, `{{cdata .a}}`},
		{"an explicit xml call is not doubled", `{{ xml .a }}`, `{{xml .a}}`},
		{"literal text is untouched", `<null/>`, `<null/>`},
		{"an assignment prints nothing either", `{{ $x := .a }}{{ $x = .b }}`, `{{$x := .a}}{{$x = .b}}`},
		{"an else-if chain is rewritten throughout", `{{ if .a }}{{ .a }}{{ else if .b }}{{ .b }}{{ else }}{{ .c }}{{ end }}`, `{{if .a}}{{.a | xml}}{{else}}{{if .b}}{{.b | xml}}{{else}}{{.c | xml}}{{end}}{{end}}`},
		{"a range decl is left alone but its body is not", `{{ range $i, $v := .a }}{{ $v }}{{ end }}`, `{{range $i, $v := .a}}{{$v | xml}}{{end}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := compile([]byte(tc.in), options{})
			require.NoError(t, err)
			assert.Equal(t, tc.want, tmpl.Root.String())
		})
	}
}

// TestInjectEscaping_ReachesAssociatedTemplates is the regression test for the
// escaping bypass: {{ define }} and {{ block }} bodies are separate parse
// trees, and walking only the main one left everything they print unescaped.
func TestInjectEscaping_ReachesAssociatedTemplates(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		lookup string
		want   string
	}{
		{
			name:   "a block body",
			in:     `<R>{{block "item" .}}<I>{{ .v }}</I>{{end}}</R>`,
			lookup: "item",
			want:   `<I>{{.v | xml}}</I>`,
		},
		{
			name:   "a define body",
			in:     `{{define "item"}}<I>{{ .v }}</I>{{end}}<R>{{template "item" .}}</R>`,
			lookup: "item",
			want:   `<I>{{.v | xml}}</I>`,
		},
		{
			// blockControl shares the parent's tree set, so this lands in the
			// same map and the one loop reaches it.
			name:   "a block nested inside a define",
			in:     `{{define "outer"}}{{block "row" .}}<I>{{ .v }}</I>{{end}}{{end}}<R>{{template "outer" .}}</R>`,
			lookup: "row",
			want:   `<I>{{.v | xml}}</I>`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := compile([]byte(tc.in), options{})
			require.NoError(t, err)

			assoc := tmpl.Lookup(tc.lookup)
			require.NotNil(t, assoc, "template %q should be associated", tc.lookup)
			assert.Equal(t, tc.want, assoc.Root.String())
		})
	}
}

// TestInjectEscaping_MutualRecursion checks that every tree in a set is
// reached, not just those the main tree names directly.
func TestInjectEscaping_MutualRecursion(t *testing.T) {
	const src = `{{define "a"}}<A>{{ .v }}</A>{{template "b" .}}{{end}}` +
		`{{define "b"}}<B>{{ .v }}</B>{{end}}` +
		`<R>{{template "a" .}}</R>`

	tmpl, err := compile([]byte(src), options{})
	require.NoError(t, err)

	assert.Equal(t, `<A>{{.v | xml}}</A>{{template "b" .}}`, tmpl.Lookup("a").Root.String())
	assert.Equal(t, `<B>{{.v | xml}}</B>`, tmpl.Lookup("b").Root.String())
}

// TestInjectEscaping_Idempotent makes a property the design relies on explicit.
// The root template is already a member of Templates(), so a second walk sees
// pipelines that already end in xml and must leave them alone.
func TestInjectEscaping_Idempotent(t *testing.T) {
	tmpl, err := compile([]byte(`<R>{{ .v }}{{block "i" .}}<I>{{ .v }}</I>{{end}}</R>`), options{})
	require.NoError(t, err)

	before := tmpl.Root.String()
	require.NoError(t, injectEscaping(tmpl))

	assert.Equal(t, before, tmpl.Root.String(), "a second pass must not double-escape")
	assert.Equal(t, `<I>{{.v | xml}}</I>`, tmpl.Lookup("i").Root.String())
}

// TestInjectEscaping_SurvivesClone pins the one place this design leans on
// stdlib internals. The injected CommandNode is built as a literal, so its
// unexported tree pointer is nil; the only thing that dereferences it is
// CommandNode.Copy, reached solely through Tree.Copy, which only html/template
// calls. xmlgen never clones — but a future refactor might, and this says what
// happens when it does.
func TestInjectEscaping_SurvivesClone(t *testing.T) {
	tmpl, err := compile([]byte(`<R>{{ .v }}</R>`), options{})
	require.NoError(t, err)

	clone, err := tmpl.Clone()
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NotPanics(t, func() {
		err = clone.Execute(&buf, map[string]any{"v": "a&b"})
	})
	require.NoError(t, err)
	assert.Equal(t, "<R>a&amp;b</R>", buf.String())
}

func TestStripBOM(t *testing.T) {
	assert.Equal(t, []byte("<R/>"), stripBOM([]byte("\xef\xbb\xbf<R/>")))
	assert.Equal(t, []byte("<R/>"), stripBOM([]byte("<R/>")))
}
