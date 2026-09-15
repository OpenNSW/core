// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// Names xmlgen binds into every template. A resolver may not use any of them.
const (
	funcEscape = "xml"
	funcRaw    = "raw"
	funcCDATA  = "cdata"
)

// helperFuncs returns the functions xmlgen defines itself: the escaping
// functions, the built-in helper library, and error stubs over the three
// text/template builtins that escape for HTML or JavaScript and so produce
// wrong output in XML.
func helperFuncs() template.FuncMap {
	return template.FuncMap{
		funcEscape: fnXML,
		funcRaw:    fnRaw,
		funcCDATA:  fnCDATA,

		"split":    fnSplit,
		"part":     fnPart,
		"join":     fnJoin,
		"date":     fnDate,
		"decimal":  fnDecimal,
		"lookup":   fnLookup,
		"coalesce": fnCoalesce,
		"trim":     fnTrim,
		"zero":     fnZero,

		"html":     disabled("html"),
		"js":       disabled("js"),
		"urlquery": disabled("urlquery"),
	}
}

func disabled(name string) func(...any) (string, error) {
	return func(...any) (string, error) {
		return "", fmt.Errorf("%w: %s escapes for HTML or JavaScript and corrupts XML; use %s, %s or %s",
			ErrHelper, name, funcEscape, funcRaw, funcCDATA)
	}
}

// fnXML renders v as XML text. The template rewriter appends it to every
// printing action, so this is the single point through which every value
// reaches the document.
func fnXML(v any) (string, error) {
	s, err := text(v)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	// strings.Builder never fails a Write, so EscapeText cannot error here.
	_ = xml.EscapeText(&b, []byte(s))
	return b.String(), nil
}

// fnRaw emits v without escaping. Use it only for a value that is already
// valid XML; the document-level check still applies, so a stray & is caught.
func fnRaw(v any) (string, error) { return text(v) }

// fnCDATA wraps v in a CDATA section. A "]]>" inside the value is split across
// two sections, which is the only way to carry one through CDATA intact.
func fnCDATA(v any) (string, error) {
	s, err := text(v)
	if err != nil {
		return "", err
	}
	return "<![CDATA[" + strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>") + "]]>", nil
}

// text converts a value to its XML text form, without escaping.
//
// A missing key and a JSON null both arrive here as nil and render as the
// empty string. A map, slice or []byte has no meaningful text form: printing
// one would put Go syntax into the document, so it is an error instead.
func text(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case json.Number:
		return t.String(), nil
	case bool:
		return strconv.FormatBool(t), nil
	case time.Time:
		return t.Format(time.RFC3339), nil
	case []byte:
		return "", fmt.Errorf("%w: []byte", ErrUnsupportedValue)
	case fmt.Stringer:
		return t.String(), nil
	}

	// Named types over a scalar kind — a caller's `type Grade string`, or a
	// float64 from a plain json.Unmarshal. 'f' with precision -1 never emits
	// an exponent, so a large quantity stays readable instead of becoming 1e+07.
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), nil
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32:
		return strconv.FormatFloat(rv.Float(), 'f', -1, 32), nil
	case reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'f', -1, 64), nil
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return "", nil
		}
		return text(rv.Elem().Interface())
	}
	return "", fmt.Errorf("%w: %T", ErrUnsupportedValue, v)
}
