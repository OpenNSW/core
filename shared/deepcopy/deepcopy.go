// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package deepcopy provides recursive deep-copy helpers for JSON-shaped data —
// values decoded into map[string]any / []any trees. Copying such a tree lets a
// caller hand it to other code (e.g. an extension or a background goroutine)
// without risking mutation of, or data races on, the original.
package deepcopy

// Value returns a deep copy of v, recursing into map[string]any and []any so
// that no map or slice is shared between the input and the result. Scalars
// (strings, numbers, bools, nil) and any other types are returned as-is: they
// are either immutable or fall outside the JSON value model handled here.
func Value(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return Map(t)
	case []any:
		cp := make([]any, len(t))
		for i, e := range t {
			cp[i] = Value(e)
		}
		return cp
	default:
		return v
	}
}

// Map returns a deep copy of m, recursing into nested values via Value. It
// returns nil if m is nil.
func Map(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = Value(v)
	}
	return cp
}
