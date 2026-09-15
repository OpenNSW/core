// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen_test

import (
	"context"
	"encoding/xml"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenNSW/core/xmlgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current output")

// renderFixture renders testdata/<name>.tmpl against testdata/<name>.json and
// compares the result with testdata/<name>.golden.xml.
func renderFixture(t *testing.T, name string) []byte {
	t.Helper()

	tmpl, err := os.ReadFile(filepath.Join("testdata", name+".tmpl"))
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	require.NoError(t, err)

	out, err := xmlgen.Generate(context.Background(), tmpl, data)
	require.NoError(t, err)
	assertNoMarkers(t, out)

	golden := filepath.Join("testdata", name+".golden.xml")
	if *update {
		require.NoError(t, os.WriteFile(golden, out, 0o600))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(out), "output drifted from %s", golden)

	return out
}

// TestGolden_Invoice is the acceptance test. The document is invented, but it
// is shaped to exercise every conversion a real export needs in one render:
// one string split across four elements, dates moved between layouts, an enum
// mapped to a number, a fixed-decimal amount, an empty value spelled as a
// marker element, a repeated element filled from a table, a computed summary,
// and text carrying an ampersand, markup, non-ASCII and embedded whitespace.
func TestGolden_Invoice(t *testing.T) {
	out := renderFixture(t, "invoice")

	var doc struct {
		XMLName xml.Name `xml:"INVOICE"`
		ID      string   `xml:"id,attr"`
		Version string   `xml:"Version"`
		Issuer  struct {
			Registration string `xml:"registration_number"`
			TaxID        string `xml:"tax_id"`
			Customer     string `xml:"Customer"`
		} `xml:"Issuer"`
		Reference struct {
			Prefix string `xml:"Reference_prefix"`
			Year   string `xml:"Reference_year"`
			Series string `xml:"Reference_series"`
			Number string `xml:"Reference_number"`
			Check  string `xml:"Reference_check"`
		} `xml:"Reference_info"`
		Header struct {
			Category string `xml:"Category"`
			Approved string `xml:"Approved"`
			Issued   string `xml:"Issued_date"`
			Due      string `xml:"Due_date"`
			TaxRate  string `xml:"Tax_rate"`
		} `xml:"Header"`
		Lines []struct {
			SKU         string `xml:"sku"`
			Description string `xml:"description"`
			Qty         string `xml:"qty"`
			UnitPrice   string `xml:"unit_price"`
			Amount      string `xml:"amount"`
		} `xml:"Line"`
		Summary struct {
			Totals struct {
				Qty     string `xml:"Total_qty"`
				Amount  string `xml:"Total_amount"`
				Average string `xml:"Average_price"`
			} `xml:"Totals"`
		} `xml:"Summary"`
		Billing struct {
			Address string `xml:"Address"`
			Notes   struct {
				Null *struct{} `xml:"null"`
			} `xml:"Notes"`
		} `xml:"Billing"`
		Adjustments struct {
			CreditNote struct {
				Null *struct{} `xml:"null"`
			} `xml:"Credit_note"`
		} `xml:"Adjustments"`
	}
	require.NoError(t, xml.Unmarshal(out, &doc))

	t.Run("one string becomes four elements", func(t *testing.T) {
		assert.Equal(t, "INV", doc.Reference.Prefix)
		assert.Equal(t, "2026", doc.Reference.Year)
		assert.Equal(t, "A", doc.Reference.Series)
		assert.Equal(t, "00042", doc.Reference.Number, "a zero-padded number keeps its padding")
		assert.Empty(t, doc.Reference.Check, "a field past the end of the value is empty, not an error")
	})

	t.Run("dates move to the layout the document uses", func(t *testing.T) {
		assert.Equal(t, "3/4/26", doc.Header.Issued, "single-digit month and day are not padded")
		assert.Equal(t, "3/19/26", doc.Header.Due)
	})

	t.Run("an enum maps to its encoded form", func(t *testing.T) {
		assert.Equal(t, "1", doc.Header.Approved)
	})

	t.Run("amounts carry their decimal places", func(t *testing.T) {
		assert.Equal(t, "15.00", doc.Header.TaxRate)
		assert.Equal(t, "1250.5618", doc.Summary.Totals.Average)
	})

	t.Run("numbers keep their exact text", func(t *testing.T) {
		assert.Equal(t, "8900", doc.Summary.Totals.Qty)
		assert.Equal(t, "11130000", doc.Summary.Totals.Amount, "no exponent notation")
		assert.Equal(t, "10000000", doc.Lines[2].Amount)
	})

	t.Run("a value outside the with block is reached through the root", func(t *testing.T) {
		assert.Equal(t, "1.0", doc.Version)
	})

	t.Run("an attribute is interpolated and escaped like any other value", func(t *testing.T) {
		assert.Equal(t, "INV-2026-00042", doc.ID)
	})

	// The fixture references no key it does not carry, so it renders clean
	// under strict keys. That is the check a template's own tests should run:
	// it is what turns a field renamed in the form schema from a silently
	// blank element into a failure that names the field.
	t.Run("the template holds up under strict keys", func(t *testing.T) {
		tmpl, err := os.ReadFile(filepath.Join("testdata", "invoice.tmpl"))
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join("testdata", "invoice.json"))
		require.NoError(t, err)

		strict, err := xmlgen.Generate(context.Background(), tmpl, data, xmlgen.WithStrictKeys())
		require.NoError(t, err)
		assert.Equal(t, string(out), string(strict), "strict keys must not change the output")
	})

	t.Run("an empty value becomes the marker element", func(t *testing.T) {
		assert.NotNil(t, doc.Billing.Notes.Null, "an empty string should render <null/>")
		assert.NotNil(t, doc.Adjustments.CreditNote.Null, "a zero should render <null/>")
	})

	t.Run("a table fills repeated elements", func(t *testing.T) {
		require.Len(t, doc.Lines, 3)
		assert.Equal(t, "Widget", doc.Lines[0].Description)
		assert.Equal(t, "Gadget & Co", doc.Lines[1].Description, "escaping applies inside a range")
		assert.Equal(t, "1250", doc.Lines[2].UnitPrice)
	})

	t.Run("text survives escaping intact", func(t *testing.T) {
		assert.Equal(t, "STANDARD & EXPRESS", doc.Header.Category)
		assert.Equal(t, "Ünïcødé Tráder <Pvt> Ltd", doc.Issuer.Customer,
			"non-ASCII and markup both round-trip")
		assert.Equal(t, "12 EXAMPLE ROAD,\t\nSAMPLE TOWN.", doc.Billing.Address,
			"a multi-line address round-trips through its numeric references")
	})

	t.Run("the escaped forms are what is on the wire", func(t *testing.T) {
		assert.Contains(t, string(out), "STANDARD &amp; EXPRESS")
		assert.Contains(t, string(out), "&lt;Pvt&gt;")
		assert.Contains(t, string(out), "&#x9;&#xA;")
	})
}

// TestGolden_Envelope covers the namespaced shape: prefixes declared at the
// root, a reserved xml: attribute, and a prefix declared inside a range body
// so it is in scope only for that subtree. Unmarshalling by namespace URI
// rather than by prefix is what proves the declarations actually bind.
func TestGolden_Envelope(t *testing.T) {
	out := renderFixture(t, "envelope")

	const soapNS = "http://schemas.xmlsoap.org/soap/envelope/"

	var env struct {
		XMLName xml.Name `xml:"http://schemas.xmlsoap.org/soap/envelope/ Envelope"`
		Header  struct {
			Trace struct {
				Lang  string `xml:"http://www.w3.org/XML/1998/namespace lang,attr"`
				ID    string `xml:"urn:example:messages id,attr"`
				Value string `xml:",chardata"`
			} `xml:"urn:example:messages Trace"`
		} `xml:"http://schemas.xmlsoap.org/soap/envelope/ Header"`
		Body struct {
			Request struct {
				Reference   string `xml:"urn:example:messages Reference"`
				SubmittedAt string `xml:"urn:example:messages SubmittedAt"`
				Documents   []struct {
					ID    string `xml:"urn:example:doc Id"`
					Title string `xml:"urn:example:doc Title"`
				} `xml:"urn:example:messages Document"`
			} `xml:"urn:example:messages SubmitRequest"`
		} `xml:"http://schemas.xmlsoap.org/soap/envelope/ Body"`
	}
	require.NoError(t, xml.Unmarshal(out, &env))

	assert.Equal(t, soapNS, env.XMLName.Space, "the root binds to the soap namespace")
	assert.Equal(t, "en", env.Header.Trace.Lang, "the reserved xml: prefix needs no declaration")
	assert.Equal(t, "T-0001", env.Header.Trace.ID)
	assert.Equal(t, "T-0001", env.Header.Trace.Value)

	assert.Equal(t, "INV-00042", env.Body.Request.Reference, "two parts of one string, joined by markup")
	assert.Equal(t, "04/03/2026", env.Body.Request.SubmittedAt)

	require.Len(t, env.Body.Request.Documents, 2)
	assert.Equal(t, "D-1", env.Body.Request.Documents[0].ID)
	assert.Equal(t, "Widget specification & notes", env.Body.Request.Documents[0].Title)
	assert.Equal(t, "Ünïcødé <attachment>", env.Body.Request.Documents[1].Title)

	// The prefix is declared on the repeated element, so it is redeclared on
	// every iteration and in scope only within each one.
	assert.Contains(t, string(out), `xmlns:d="urn:example:doc"`)
}

// TestGolden_TemplatesValidate checks every committed template the way a
// registry or a CI sweep would, without data.
func TestGolden_TemplatesValidate(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("testdata", "*.tmpl"))
	require.NoError(t, err)
	require.NotEmpty(t, matches)

	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			tmpl, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NoError(t, xmlgen.Validate(tmpl))
		})
	}
}
