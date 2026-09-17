// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package jsonpointer implements a small, intentionally restricted subset of
// RFC 6901 (JSON Pointer): resolving or setting a value inside a nested
// map[string]any document. It deliberately excludes arrays — Get and Set
// only ever walk into map[string]any, and stop (without error) the moment
// they meet anything else, including a slice. The pointer syntax itself is
// full RFC 6901 (including "~0"/"~1" escaping), so array-index support could
// be added later without changing how a pointer string is written.
package jsonpointer

import (
	"fmt"
	"strings"
)

// unescaper reverses RFC 6901's escaping ("~0" -> "~", "~1" -> "/"). Order
// within the pair list doesn't matter here: strings.Replacer performs a
// single non-overlapping left-to-right scan, and at any "~" the following
// character is always exactly "0" or "1", never both, so there's no
// ambiguity between the two rules to resolve by ordering them.
var unescaper = strings.NewReplacer("~1", "/", "~0", "~")

// parse splits a JSON Pointer into its unescaped segments. The empty string
// (the RFC 6901 root pointer) parses to zero segments and no error; any
// other string must start with "/".
func parse(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if pointer[0] != '/' {
		return nil, fmt.Errorf("jsonpointer: %q does not start with '/'", pointer)
	}
	raw := strings.Split(pointer[1:], "/")
	segments := make([]string, len(raw))
	for i, s := range raw {
		for j := 0; j < len(s); j++ {
			if s[j] == '~' && (j+1 == len(s) || (s[j+1] != '0' && s[j+1] != '1')) {
				return nil, fmt.Errorf("jsonpointer: %q has an invalid escape sequence (a '~' must be followed by '0' or '1')", pointer)
			}
		}
		segments[i] = unescaper.Replace(s)
	}
	return segments, nil
}

// Segments splits pointer into its unescaped RFC 6901 segments, for callers
// that need to build something else out of the path (e.g. a SQL JSON-path
// expression) rather than resolve it against a document. Returns an error
// under the same conditions as a failed Valid check.
func Segments(pointer string) ([]string, error) {
	segments, err := parse(pointer)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("jsonpointer: %q does not refer to a value", pointer)
	}
	return segments, nil
}

// Valid reports whether pointer is syntactically valid RFC 6901 syntax and
// refers to something other than the document root (i.e. is non-empty).
// Intended for config-time validation, so a malformed pointer is rejected
// when it's authored rather than silently never resolving at runtime.
func Valid(pointer string) bool {
	segments, err := parse(pointer)
	return err == nil && len(segments) > 0
}

// Get resolves pointer against doc, walking only map[string]any values. It
// reports ok=false if the pointer is malformed or empty, any segment is
// missing, or the walk meets anything that isn't a map[string]any —
// including an array — before the pointer is exhausted.
func Get(doc map[string]any, pointer string) (value any, ok bool) {
	segments, err := parse(pointer)
	if err != nil || len(segments) == 0 {
		return nil, false
	}

	var cur any = doc
	for _, seg := range segments {
		m, isMap := cur.(map[string]any)
		if !isMap {
			return nil, false
		}
		v, exists := m[seg]
		if !exists {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// Set writes value into doc at pointer, creating intermediate
// map[string]any objects as needed, and reports whether it succeeded. doc
// is left unchanged if pointer is malformed or empty, or if an existing
// intermediate segment holds something other than a map[string]any
// (including an array) — Set never overwrites a non-object value to force
// a path through it.
func Set(doc map[string]any, pointer string, value any) (ok bool) {
	segments, err := parse(pointer)
	if err != nil || len(segments) == 0 {
		return false
	}

	cur := doc
	for _, seg := range segments[:len(segments)-1] {
		next, exists := cur[seg]
		if !exists {
			child := map[string]any{}
			cur[seg] = child
			cur = child
			continue
		}
		child, isMap := next.(map[string]any)
		if !isMap {
			return false
		}
		cur = child
	}
	cur[segments[len(segments)-1]] = value
	return true
}
