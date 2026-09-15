// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen_test

import (
	"context"
	"testing"

	"github.com/OpenNSW/core/xmlgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generate(t *testing.T, tmpl string, opts ...xmlgen.Option) ([]byte, error) {
	t.Helper()
	return xmlgen.Generate(context.Background(), []byte(tmpl), map[string]any{}, opts...)
}

func TestDocumentCheck_Structure(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		want error
	}{
		{"a single root is accepted", `<R><A/></R>`, nil},
		{"a declaration and comment before the root are accepted", "<?xml version=\"1.0\"?>\n<!-- note -->\n<R/>", nil},
		{"whitespace after the root is accepted", "<R/>\n", nil},

		{"an unclosed element is refused", `<R><A></R>`, xmlgen.ErrMalformedXML},
		{"a mismatched end tag is refused", `<R><A></B></R>`, xmlgen.ErrMalformedXML},
		{"an unbalanced end tag is refused", `<R></R></R>`, xmlgen.ErrMalformedXML},
		{"a bare ampersand is refused", `<R>a & b</R>`, xmlgen.ErrMalformedXML},
		{"text before the root is refused", `hello <R/>`, xmlgen.ErrMalformedXML},
		{"an undefined entity is refused", `<R>&nbsp;</R>`, xmlgen.ErrMalformedXML},

		// encoding/xml accepts every one of these on its own.
		{"a second root is refused", `<R/><S/>`, xmlgen.ErrMultipleRoots},
		{"content after the root is refused", `<R/><S>x</S>`, xmlgen.ErrMultipleRoots},
		{"no root at all is refused", `<!-- just a comment -->`, xmlgen.ErrMultipleRoots},
		{"an empty document is refused", ``, xmlgen.ErrMultipleRoots},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := generate(t, tc.tmpl)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestDocumentCheck_Doctype(t *testing.T) {
	// Go's decoder ignores the internal subset, so an entity declared here is
	// inert to it — but the Java or libxml parser at the far end will expand
	// it. Refusing the DOCTYPE outright is the only safe reading.
	_, err := generate(t, `<!DOCTYPE R [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><R/>`)
	require.ErrorIs(t, err, xmlgen.ErrDoctypeNotAllowed)
}

func TestDocumentCheck_Namespaces(t *testing.T) {
	t.Run("declared prefixes are accepted", func(t *testing.T) {
		_, err := generate(t, `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body/></soap:Envelope>`)
		require.NoError(t, err)
	})

	t.Run("a nested declaration is in scope for its subtree", func(t *testing.T) {
		_, err := generate(t, `<R><a:B xmlns:a="urn:a"><a:C/></a:B></R>`)
		require.NoError(t, err)
	})

	t.Run("a default namespace needs no prefix", func(t *testing.T) {
		_, err := generate(t, `<R xmlns="urn:wco:datamodel"><A/></R>`)
		require.NoError(t, err)
	})

	t.Run("the reserved xml prefix is always in scope", func(t *testing.T) {
		_, err := generate(t, `<R xml:lang="en" xml:space="preserve"/>`)
		require.NoError(t, err)
	})

	t.Run("a typo'd prefix is refused", func(t *testing.T) {
		_, err := generate(t, `<soap:Envelope xmlns:soap="urn:x"><soapp:Body/></soap:Envelope>`)
		require.ErrorIs(t, err, xmlgen.ErrUndeclaredNamespace)
		assert.Contains(t, err.Error(), "soapp")
	})

	t.Run("an undeclared prefix on an attribute is refused", func(t *testing.T) {
		_, err := generate(t, `<R xsi:type="x"/>`)
		require.ErrorIs(t, err, xmlgen.ErrUndeclaredNamespace)
	})

	t.Run("a prefix used after its scope closes is refused", func(t *testing.T) {
		_, err := generate(t, `<R><a:B xmlns:a="urn:a"/><a:C/></R>`)
		require.ErrorIs(t, err, xmlgen.ErrUndeclaredNamespace)
	})

	t.Run("the check can be turned off for a fragment", func(t *testing.T) {
		_, err := generate(t, `<soapp:Body/>`, xmlgen.SkipNamespaceCheck())
		require.NoError(t, err)
	})
}

func TestDocumentCheck_Encoding(t *testing.T) {
	t.Run("a UTF-8 declaration is accepted", func(t *testing.T) {
		_, err := generate(t, `<?xml version="1.0" encoding="UTF-8"?><R/>`)
		require.NoError(t, err)
	})

	// Shipping UTF-8 bytes under a Latin-1 declaration is what mangles a
	// Sinhala or Tamil name at the far end.
	t.Run("a non-UTF-8 declaration is refused", func(t *testing.T) {
		_, err := generate(t, `<?xml version="1.0" encoding="ISO-8859-1"?><R/>`)
		require.ErrorIs(t, err, xmlgen.ErrMalformedXML)
	})
}

func TestDocumentCheck_BOM(t *testing.T) {
	// A BOM survives editing a template on Windows and would otherwise sit in
	// front of the XML declaration.
	out, err := xmlgen.Generate(context.Background(),
		[]byte("\xef\xbb\xbf<?xml version=\"1.0\"?><R/>"), nil)
	require.NoError(t, err)
	assert.False(t, len(out) > 3 && out[0] == 0xef && out[1] == 0xbb && out[2] == 0xbf,
		"the BOM must not reach the document")
	assert.Equal(t, `<?xml version="1.0"?><R/>`, string(out))
}
