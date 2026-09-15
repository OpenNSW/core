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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := compile([]byte(tc.in), options{})
			require.NoError(t, err)
			assert.Equal(t, tc.want, tmpl.Root.String())
		})
	}
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
