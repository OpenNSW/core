// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenNSW/core/xmlgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The happy-path fixtures prove the transforms work. These prove the failures
// are legible on a document of realistic size — an error that says only
// "invalid XML" is of no use against a hundred-element declaration, so each
// case also asserts that the message names the thing that went wrong.

func invoiceData(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "invoice.json"))
	require.NoError(t, err)
	return raw
}

// TestGolden_InvalidTemplates renders each broken template in
// testdata/invalid/ against the real invoice data. Every one of them is a
// mistake somebody could plausibly commit.
func TestGolden_InvalidTemplates(t *testing.T) {
	data := invoiceData(t)

	tests := []struct {
		file string
		why  string
		want error
		says string
	}{
		{
			file: "two-roots.tmpl",
			why:  "a range at the top level with nothing wrapping it",
			want: xmlgen.ErrMultipleRoots,
			says: "Line",
		},
		{
			file: "unclosed-element.tmpl",
			why:  "an element that is never closed",
			want: xmlgen.ErrMalformedXML,
			says: "Header",
		},
		{
			file: "undeclared-prefix.tmpl",
			why:  "a prefix typed one letter wrong, which encoding/xml accepts on its own",
			want: xmlgen.ErrUndeclaredNamespace,
			says: "soapp",
		},
		{
			file: "doctype.tmpl",
			why:  "a DOCTYPE, which Go ignores but a receiving parser will not",
			want: xmlgen.ErrDoctypeNotAllowed,
		},
		{
			file: "unknown-function.tmpl",
			why:  "a resolver the caller never supplied",
			want: xmlgen.ErrParseTemplate,
			says: "codelist",
		},
		{
			file: "syntax-error.tmpl",
			why:  "an if with no end",
			want: xmlgen.ErrParseTemplate,
		},
		{
			file: "raw-bypass.tmpl",
			why:  "raw opts out of escaping, but not out of producing a valid document",
			want: xmlgen.ErrMalformedXML,
		},
		{
			file: "object-as-text.tmpl",
			why:  "printing a whole object where text belongs",
			want: xmlgen.ErrUnsupportedValue,
			says: "map",
		},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			tmpl, err := os.ReadFile(filepath.Join("testdata", "invalid", tc.file))
			require.NoError(t, err)

			out, err := xmlgen.Generate(context.Background(), tmpl, data)
			require.Error(t, err, "%s: %s", tc.file, tc.why)
			require.ErrorIs(t, err, tc.want)
			assert.Nil(t, out, "a failed render must return no document")
			if tc.says != "" {
				assert.Contains(t, err.Error(), tc.says,
					"the message should name what went wrong so it can be found in the template")
			}
		})
	}
}

// TestGolden_InvalidTemplatesFailValidation checks that the mistakes which can
// be caught without data are caught at load time. This is the difference
// between a template failing when it is stored and failing when a citizen
// presses submit.
func TestGolden_InvalidTemplatesFailValidation(t *testing.T) {
	// Only the parse-time faults are visible without rendering; the rest
	// depend on the data and can only surface from Generate.
	atLoadTime := map[string]bool{
		"unknown-function.tmpl": true,
		"syntax-error.tmpl":     true,
	}

	matches, err := filepath.Glob(filepath.Join("testdata", "invalid", "*.tmpl"))
	require.NoError(t, err)
	require.NotEmpty(t, matches)

	for _, path := range matches {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			tmpl, err := os.ReadFile(path)
			require.NoError(t, err)

			err = xmlgen.Validate(tmpl)
			if atLoadTime[name] {
				require.ErrorIs(t, err, xmlgen.ErrParseTemplate)
				return
			}
			require.NoError(t, err, "Validate sees no data, so it cannot catch this one")
		})
	}
}

// patchedInvoice returns the real invoice fixture with one value replaced.
//
// Patching the fixture rather than keeping a near-duplicate JSON file per case
// keeps the broken value next to the assertion, and means these cases cannot
// silently drift out of step with invoice.json.
func patchedInvoice(t *testing.T, patch func(invoice map[string]any)) []byte {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(invoiceData(t)))
	dec.UseNumber()
	var doc map[string]any
	require.NoError(t, dec.Decode(&doc))

	invoice, ok := doc["invoice"].(map[string]any)
	require.True(t, ok, "fixture shape changed: invoice.json has no invoice object")
	patch(invoice)

	out, err := json.Marshal(doc)
	require.NoError(t, err)
	return out
}

// TestGolden_InvalidData renders the real invoice template against data broken
// at exactly one point, which is how these faults arrive in practice: a form
// field renamed, a date arriving in the wrong format, a value that should be a
// scalar carrying an object.
func TestGolden_InvalidData(t *testing.T) {
	tmpl, err := os.ReadFile(filepath.Join("testdata", "invoice.tmpl"))
	require.NoError(t, err)

	tests := []struct {
		name  string
		patch func(map[string]any)
		want  error
		says  string
	}{
		{
			name:  "a date in the wrong layout",
			patch: func(inv map[string]any) { inv["issued_on"] = "04/03/2026" },
			want:  xmlgen.ErrHelper,
			says:  "2006-01-02",
		},
		{
			name:  "a number that is not numeric",
			patch: func(inv map[string]any) { inv["tax_rate"] = "fifteen" },
			want:  xmlgen.ErrHelper,
			says:  "fifteen",
		},
		{
			name: "an object where text belongs",
			patch: func(inv map[string]any) {
				inv["customer_name"] = map[string]any{"first": "A", "last": "B"}
			},
			want: xmlgen.ErrUnsupportedValue,
		},
		{
			name:  "a table that is not a table",
			patch: func(inv map[string]any) { inv["lines"] = map[string]any{"sheet": "not rows"} },
			want:  xmlgen.ErrRender,
			says:  "range",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := xmlgen.Generate(context.Background(), tmpl, patchedInvoice(t, tc.patch))
			require.Error(t, err)
			require.ErrorIs(t, err, tc.want)
			assert.Nil(t, out)
			if tc.says != "" {
				assert.Contains(t, err.Error(), tc.says)
			}
		})
	}

	t.Run("data that is not JSON at all", func(t *testing.T) {
		_, err := xmlgen.Generate(context.Background(), tmpl, []byte(`{"invoice":`))
		require.ErrorIs(t, err, xmlgen.ErrInvalidData)
	})

	// The same template and the same gap, with strict keys on: what renders as
	// an empty element in production becomes a named error in a template's own
	// tests.
	t.Run("a renamed field is silent by default and named under strict keys", func(t *testing.T) {
		data := patchedInvoice(t, func(inv map[string]any) {
			delete(inv, "category")
		})

		out, err := xmlgen.Generate(context.Background(), tmpl, data)
		require.NoError(t, err, "by default a missing field is simply empty")
		assert.Contains(t, string(out), "<Category></Category>")

		_, err = xmlgen.Generate(context.Background(), tmpl, data, xmlgen.WithStrictKeys())
		require.ErrorIs(t, err, xmlgen.ErrMissingKey)
		assert.Contains(t, err.Error(), "category")
	})
}
