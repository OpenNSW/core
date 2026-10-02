// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package htmlgen

import (
	"encoding/json"
	"fmt"
	"html/template"
	"reflect"
	"strconv"
	"time"
)

// funcText is the name htmlgen binds to its value conversion. The template
// rewriter appends it to every printing action.
const funcText = "text"

// helperFuncs returns the functions htmlgen defines itself: the value
// conversion.
func helperFuncs() template.FuncMap {
	return template.FuncMap{
		funcText: fnText,
	}
}

// fnText converts v to the text html/template then escapes. The template
// rewriter appends it to every printing action, so this is the single point
// through which every value reaches the document.
//
// html/template's typed strings pass through unchanged, so a caller that
// deliberately hands over trusted markup or a data: URL from Go code keeps
// it. JSON data never decodes to those types.
func fnText(v any) (any, error) {
	switch v.(type) {
	case template.HTML, template.HTMLAttr, template.CSS, template.JS,
		template.JSStr, template.URL, template.Srcset:
		return v, nil
	}
	return text(v)
}

// text converts a value to its text form, without escaping.
//
// A missing key and a JSON null both arrive here as nil and render as the
// empty string. A map, slice or []byte has no meaningful text form: printing
// one would put Go syntax into the document, so it is an error instead.
//
// The rules match xmlgen's, so a value renders the same in either package.
func text(v any) (string, error) { return textDepth(v, 0) }

// maxIndirect bounds how far textDepth follows pointers and interfaces.
//
// Without it a caller-supplied pointer cycle recurses until the goroutine
// stack overflows, which Go treats as fatal and recover cannot catch — it
// would take the process down rather than fail the render. Data decoded from
// JSON cannot form a cycle, but a caller may hand over native Go values, so
// the bound has to be here. Nothing legitimate nests this deeply.
const maxIndirect = 32

func textDepth(v any, depth int) (string, error) {
	if depth > maxIndirect {
		return "", fmt.Errorf("%w: value is nested more than %d levels deep", ErrUnsupportedValue, maxIndirect)
	}
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
		return textDepth(rv.Elem().Interface(), depth+1)
	}
	return "", fmt.Errorf("%w: %T", ErrUnsupportedValue, v)
}
