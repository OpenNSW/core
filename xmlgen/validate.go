// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// checkDocument verifies that rendered output is a well-formed XML document.
//
// encoding/xml on its own gives fragment well-formedness: it catches an
// unbalanced tag or a bare &, but it accepts several documents a receiving
// system will not. It permits more than one root element, it permits text
// before the root, and — as its own documentation states — it does not reject
// a namespace prefix that was never declared, recording the prefix as the
// namespace instead. Each of those is checked here.
//
// The check reads the bytes and never rewrites them, so output stays
// byte-stable and remains valid to sign.
func checkDocument(doc []byte, opts options) error {
	dec := xml.NewDecoder(bytes.NewReader(doc))

	// RawToken rather than Token: it leaves namespace prefixes unresolved,
	// which is the only way to see that one was never declared. It also does
	// not check that start and end elements match, so that is tracked here.
	var open []xml.Name
	var scopes []map[string]bool
	roots := 0

	for {
		tok, err := dec.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMalformedXML, err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if len(open) == 0 {
				roots++
				if roots > 1 {
					return fmt.Errorf("%w: found a second root element <%s>", ErrMultipleRoots, name(t.Name))
				}
			}
			scopes = append(scopes, declaredPrefixes(t.Attr))
			if !opts.skipNamespaceCheck {
				if err := checkPrefixes(t, scopes); err != nil {
					return err
				}
			}
			open = append(open, t.Name)

		case xml.EndElement:
			if len(open) == 0 {
				return fmt.Errorf("%w: unexpected </%s>", ErrMalformedXML, name(t.Name))
			}
			if last := open[len(open)-1]; last != t.Name {
				return fmt.Errorf("%w: <%s> closed by </%s>", ErrMalformedXML, name(last), name(t.Name))
			}
			open = open[:len(open)-1]
			scopes = scopes[:len(scopes)-1]

		case xml.CharData:
			if len(open) == 0 && len(bytes.TrimSpace(t)) > 0 {
				return fmt.Errorf("%w: text outside the root element", ErrMalformedXML)
			}

		case xml.Directive:
			if bytes.HasPrefix(bytes.TrimSpace(t), []byte("DOCTYPE")) {
				return ErrDoctypeNotAllowed
			}
		}
	}

	if len(open) > 0 {
		return fmt.Errorf("%w: <%s> was never closed", ErrMalformedXML, name(open[len(open)-1]))
	}
	if roots != 1 {
		return fmt.Errorf("%w: found %d", ErrMultipleRoots, roots)
	}
	return nil
}

// declaredPrefixes collects the namespace prefixes an element declares.
func declaredPrefixes(attrs []xml.Attr) map[string]bool {
	var declared map[string]bool
	for _, a := range attrs {
		if a.Name.Space != "xmlns" {
			continue
		}
		if declared == nil {
			declared = make(map[string]bool, len(attrs))
		}
		declared[a.Name.Local] = true
	}
	return declared
}

// checkPrefixes verifies that the element's own prefix, and every prefixed
// attribute's, is declared somewhere in the enclosing scopes.
func checkPrefixes(el xml.StartElement, scopes []map[string]bool) error {
	if err := checkPrefix(el.Name.Space, scopes); err != nil {
		return fmt.Errorf("%w on element <%s>", err, name(el.Name))
	}
	for _, a := range el.Attr {
		// xmlns:x="..." declares a prefix rather than using one.
		if a.Name.Space == "xmlns" {
			continue
		}
		if err := checkPrefix(a.Name.Space, scopes); err != nil {
			return fmt.Errorf("%w on attribute %s of <%s>", err, name(a.Name), name(el.Name))
		}
	}
	return nil
}

func checkPrefix(prefix string, scopes []map[string]bool) error {
	// No prefix is the default namespace, and xml/xmlns are bound by the
	// specification without being declared.
	if prefix == "" || prefix == "xml" || prefix == "xmlns" {
		return nil
	}
	for i := len(scopes) - 1; i >= 0; i-- {
		if scopes[i][prefix] {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrUndeclaredNamespace, prefix)
}

func name(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// stripBOM removes a leading UTF-8 byte order mark. One survives editing a
// template on Windows and would otherwise sit in front of the XML declaration.
func stripBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
}

// looksLikeMissingKey reports whether an execution error came from
// missingkey=error, which text/template reports only as a message.
func looksLikeMissingKey(err error) bool {
	s := err.Error()
	return strings.Contains(s, "map has no entry for key") || strings.Contains(s, "nil data; no entry for key")
}
