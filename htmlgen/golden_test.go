// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package htmlgen_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenNSW/core/htmlgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current output")

// renderFixture renders testdata/<name>.tmpl against testdata/<name>.json and
// compares the result with testdata/<name>.golden.html.
func renderFixture(t *testing.T, name string) string {
	t.Helper()

	tmpl, err := os.ReadFile(filepath.Join("testdata", name+".tmpl"))
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	require.NoError(t, err)

	out, err := htmlgen.Generate(context.Background(), tmpl, data)
	require.NoError(t, err)
	for _, marker := range []string{"<no value>", "<nil>", "<invalid reflect.Value>", "ZgotmplZ"} {
		assert.NotContains(t, string(out), marker, "template placeholder leaked into the document")
	}

	golden := filepath.Join("testdata", name+".golden.html")
	if *update {
		require.NoError(t, os.WriteFile(golden, out, 0o600))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(out), "output drifted from %s", golden)

	return string(out)
}

// TestGolden_Permit is the acceptance test. The permit is invented, but it is
// shaped like a real issued document: a full A4 page with print CSS, nested
// data assembled by the caller from several sources, multi-line addresses, an amount formatted exactly, a list joined, an absent
// value given a fallback, a repeated row filled from a table, a date moved
// between layouts, a URL with a query string, and text carrying an
// ampersand, markup, quotes and non-ASCII.
func TestGolden_Permit(t *testing.T) {
	out := renderFixture(t, "permit")

	// The properties the golden file pins, spelled out so a regenerated
	// golden cannot silently lose one.
	assert.Contains(t, out, "@page { size: A4; margin: 12mm; }", "print CSS reaches the document untouched")
	assert.Contains(t, out, "Operate under &lt;Schedule B&gt; &amp; local rules", "markup in data is escaped")
	assert.Contains(t, out, "4 Rue de &lt;Rivoli&gt;", "markup in nested data is escaped")
	assert.Contains(t, out, "Thé &amp; Co", "non-ASCII survives, ampersand is escaped")
	assert.Contains(t, out, "12 Main Street\nSpringfield", "a multi-line value keeps its line break for pre-line")
	assert.Contains(t, out, "10000000.00", "an amount is formatted exactly, without an exponent")
	assert.Contains(t, out, "DOC-0001, DOC-0002")
	assert.Contains(t, out, "Not specified", "a JSON null falls back")
	assert.Contains(t, out, "<li>A-02 &middot; quantity 80</li>")
	assert.Contains(t, out, "Issued on 01/10/2026")
	assert.Contains(t, out, `href="https://example.org/verify?id=PRM-2026-00555&amp;v=1"`)
}

func TestGolden_TemplatesValidate(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("testdata", "*.tmpl"))
	require.NoError(t, err)
	require.NotEmpty(t, matches)
	for _, path := range matches {
		tmpl, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.NoError(t, htmlgen.Validate(tmpl), path)
	}
}

// TestGolden_InvalidTemplates pins the error each broken template in
// testdata/invalid/ produces, from both Validate and Generate. Every one is a
// mistake a template author can actually make.
func TestGolden_InvalidTemplates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "permit.json"))
	require.NoError(t, err)

	cases := map[string]error{
		"syntax-error.tmpl":       htmlgen.ErrParseTemplate,
		"unknown-function.tmpl":   htmlgen.ErrParseTemplate,
		"branch-context.tmpl":     htmlgen.ErrUnsafeTemplate,
		"unclosed-attribute.tmpl": htmlgen.ErrUnsafeTemplate,
	}

	matches, err := filepath.Glob(filepath.Join("testdata", "invalid", "*.tmpl"))
	require.NoError(t, err)
	require.Len(t, matches, len(cases), "every invalid fixture needs an expected error here")

	for file, want := range cases {
		t.Run(file, func(t *testing.T) {
			tmpl, err := os.ReadFile(filepath.Join("testdata", "invalid", file))
			require.NoError(t, err)

			require.ErrorIs(t, htmlgen.Validate(tmpl), want, "Validate")
			_, err = htmlgen.Generate(context.Background(), tmpl, data)
			require.ErrorIs(t, err, want, "Generate")
		})
	}
}
