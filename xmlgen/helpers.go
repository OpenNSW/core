// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen

// The built-in helper library: the conversions a document template needs to
// turn form data into the shapes a receiving system expects. They are pure and
// deterministic, always available, and take no configuration — which is what
// keeps Generate(ctx, tmpl, data) sufficient for most templates.
//
// Anything requiring I/O is a resolver instead; see resolvers.go.

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// number converts a value to a float64 for the numeric helpers.
func number(v any) (float64, error) {
	s, err := text(v)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is not a number", ErrHelper, s)
	}
	return f, nil
}

// fnSplit splits a value on sep. Intended for {{ range split .codes "," }}.
func fnSplit(v any, sep string) ([]string, error) {
	s, err := text(v)
	if err != nil {
		return nil, err
	}
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, sep), nil
}

// fnPart returns the nth field of v split on sep, counting from zero.
//
// An index past the end returns the empty string rather than failing: a
// document that carries a reference in four elements should still render when
// the form holds a shorter one, with the missing element simply empty.
func fnPart(v any, sep string, n int) (string, error) {
	s, err := text(v)
	if err != nil {
		return "", err
	}
	if s == "" || n < 0 {
		return "", nil
	}
	fields := strings.Split(s, sep)
	if n >= len(fields) {
		return "", nil
	}
	return fields[n], nil
}

// fnJoin concatenates a slice with sep.
func fnJoin(v any, sep string) (string, error) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return "", nil
	}
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return "", fmt.Errorf("%w: join needs a slice, got %T", ErrHelper, v)
	}
	parts := make([]string, 0, rv.Len())
	for i := range rv.Len() {
		s, err := text(rv.Index(i).Interface())
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, sep), nil
}

// fnDate reparses a date from one layout into another, using Go reference
// layouts — date .declared_on "2006-01-02" "1/2/06" gives 3/4/26.
//
// A value that does not match in is an error rather than a guess: silently
// emitting some other date onto a declaration is worse than failing.
func fnDate(v any, in, out string) (string, error) {
	s, err := text(v)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(s) == "" {
		return "", nil
	}
	t, err := time.Parse(in, strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("%w: date %q does not match layout %q", ErrHelper, s, in)
	}
	return t.Format(out), nil
}

// fnDecimal formats a number with a fixed number of decimal places.
func fnDecimal(v any, places int) (string, error) {
	if places < 0 {
		return "", fmt.Errorf("%w: decimal places must not be negative", ErrHelper)
	}
	f, err := number(v)
	if err != nil {
		return "", err
	}
	return strconv.FormatFloat(f, 'f', places, 64), nil
}

// fnLookup substitutes v using alternating key/value arguments —
// lookup .certified "yes" "1" "no" "0".
//
// A value with no matching key passes through unchanged, matching the `map`
// semantics of the x-xml import vocabulary this often mirrors. That keeps a
// newly added enum member rendering rather than breaking the document, at the
// cost of letting an unmapped value reach the output.
func fnLookup(v any, pairs ...any) (any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("%w: lookup needs an even number of key/value arguments, got %d", ErrHelper, len(pairs))
	}
	key, err := text(v)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(pairs); i += 2 {
		k, err := text(pairs[i])
		if err != nil {
			return nil, err
		}
		if k == key {
			return pairs[i+1], nil
		}
	}
	return v, nil
}

// fnCoalesce returns the first argument that is neither nil nor empty.
func fnCoalesce(vals ...any) (any, error) {
	for _, v := range vals {
		if v == nil {
			continue
		}
		s, err := text(v)
		if err != nil {
			// A non-scalar is not empty; hand it back and let xml reject it
			// where the error can name the element.
			return v, nil
		}
		if s != "" {
			return v, nil
		}
	}
	return nil, nil
}

// fnZero reports whether a value should be treated as absent: nil, empty,
// false, or numerically zero.
//
// It exists because data decoded with UseNumber carries numbers as
// json.Number, which is a string underneath, so {{ if .quantity }} is true
// even when the quantity is 0. Write {{ if zero .gain }} instead:
//
//	<Gain>{{ if zero .gain }}<null/>{{ else }}{{ .gain }}{{ end }}</Gain>
func fnZero(v any) (bool, error) {
	if v == nil {
		return true, nil
	}
	if b, ok := v.(bool); ok {
		return !b, nil
	}
	s, err := text(v)
	if err != nil {
		// A map or slice is not a scalar zero. Report it as non-zero and let
		// xml reject it where the error can name the element.
		return false, nil //nolint:nilerr // deliberate: see comment above
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return true, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f == 0, nil
	}
	return false, nil
}

// fnTrim removes leading and trailing whitespace.
func fnTrim(v any) (string, error) {
	s, err := text(v)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(s), nil
}
