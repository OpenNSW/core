// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package filefields reads which values in a task's data are stored files, as
// the task's render config declares them, and rewrites those values.
//
// A render config lists its file fields at the top level:
//
//	"files": ["applicantform.invoice", "userform.documents[*].file"]
//
// Each entry is a path into the task's data:
//
//	path    = segment { "." segment }
//	segment = name { "[*]" }
//	name    = 1*( letter | digit | "_" | "-" )
//
// A name steps into an object's field, and each "[*]" steps into every element
// of an array, so arrays nest to any depth.
//
// The data must have the shape its declaration says. A field that holds a list
// of files is declared with "[*]", one that holds one file without it. Where a
// path meets another shape, Replace fails rather than leave a file unprotected.
//
// The package is pure: it does no I/O and knows nothing of what the values
// are rewritten to.
package filefields

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Path is one declared file field.
type Path struct {
	segments []segment
}

// segment is a name followed by the number of "[*]" after it.
type segment struct {
	name   string
	arrays int
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ErrShape means a declared path met a value of another shape than its
// declaration says, such as a list where one file was declared.
var ErrShape = errors.New("file field does not match its declaration")

// Parse returns the file fields renderConfig declares, in the order it lists
// them. A render config without "files" declares none. A path that does not
// parse, or is declared twice, is an error, not skipped: a file field left out
// would go unprotected, and one listed twice would be rewritten twice.
func Parse(renderConfig json.RawMessage) ([]Path, error) {
	if len(renderConfig) == 0 {
		return nil, nil
	}
	var decl struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(renderConfig, &decl); err != nil {
		return nil, fmt.Errorf("filefields: decode files: %w", err)
	}
	paths := make([]Path, 0, len(decl.Files))
	declared := make(map[string]bool, len(decl.Files))
	for _, s := range decl.Files {
		p, err := parsePath(s)
		if err != nil {
			return nil, err
		}
		if declared[p.String()] {
			return nil, fmt.Errorf("filefields: file field %q is declared twice", p)
		}
		declared[p.String()] = true
		paths = append(paths, p)
	}
	return paths, nil
}

func parsePath(s string) (Path, error) {
	parts := strings.Split(s, ".")
	segments := make([]segment, 0, len(parts))
	for _, part := range parts {
		name, arrays := part, 0
		for strings.HasSuffix(name, "[*]") {
			name, arrays = strings.TrimSuffix(name, "[*]"), arrays+1
		}
		if !namePattern.MatchString(name) {
			return Path{}, fmt.Errorf("filefields: file field %q: %q is not a name followed by any number of [*]", s, part)
		}
		segments = append(segments, segment{name: name, arrays: arrays})
	}
	return Path{segments: segments}, nil
}

// String returns p as it is written in a render config.
func (p Path) String() string {
	parts := make([]string, len(p.segments))
	for i, seg := range p.segments {
		parts[i] = seg.name + strings.Repeat("[*]", seg.arrays)
	}
	return strings.Join(parts, ".")
}

// Rebase returns p relative to the value at the top-level field prefix, for
// data that holds only that value: "applicantform.invoice" rebased on
// "applicantform" is "invoice". ok is false when p does not lie inside prefix.
// An empty prefix returns p unchanged.
func (p Path) Rebase(prefix string) (rebased Path, ok bool) {
	if prefix == "" {
		return p, true
	}
	if len(p.segments) < 2 || p.segments[0] != (segment{name: prefix}) {
		return Path{}, false
	}
	return Path{segments: p.segments[1:]}, true
}

// Replace replaces each non-empty string at paths in data with fn's result,
// in place, so the caller passes its own copy. data must be JSON-decoded
// (objects as map[string]any, arrays as []any).
//
// A missing value, null or an empty string is skipped: there is no file there.
// Any other value of another shape than the path declares is an error that
// wraps ErrShape and names where the path met it. So is the first error from
// fn, which stops the walk and is returned wrapped, so errors.Is sees it.
func Replace(data any, paths []Path, fn func(string) (string, error)) error {
	for _, p := range paths {
		if len(p.segments) == 0 {
			continue
		}
		if err := replaceIn(data, p.segments, "", fn); err != nil {
			return fmt.Errorf("filefields: %s: %w", p, err)
		}
	}
	return nil
}

// replaceIn replaces the strings at segments under node, an object. at is the
// part of the path that led to node, for errors.
func replaceIn(node any, segments []segment, at string, fn func(string) (string, error)) error {
	if node == nil {
		return nil
	}
	obj, ok := node.(map[string]any)
	if !ok {
		if at == "" {
			at = "the task data"
		}
		return shapeError(at, node, "not an object")
	}
	seg := segments[0]
	if at != "" {
		at += "."
	}
	at += seg.name
	v, ok := obj[seg.name]
	if !ok {
		return nil
	}
	replaced, err := replaceValue(v, seg.arrays, segments[1:], at, fn)
	if err != nil {
		return err
	}
	obj[seg.name] = replaced
	return nil
}

// replaceValue returns v with the strings under it replaced: it steps into
// arrays levels of array, then follows rest.
func replaceValue(v any, arrays int, rest []segment, at string, fn func(string) (string, error)) (any, error) {
	if v == nil {
		return v, nil
	}
	if arrays > 0 {
		arr, ok := v.([]any)
		if !ok {
			if _, one := v.(string); one && arrays == 1 && len(rest) == 0 {
				return nil, shapeError(at, v, "not a list; declare it without [*]")
			}
			return nil, shapeError(at, v, "not a list")
		}
		at += "[*]"
		for i, el := range arr {
			replaced, err := replaceValue(el, arrays-1, rest, at, fn)
			if err != nil {
				return nil, err
			}
			arr[i] = replaced
		}
		return arr, nil
	}
	if len(rest) > 0 {
		return v, replaceIn(v, rest, at, fn)
	}
	switch value := v.(type) {
	case string:
		if value == "" {
			return v, nil
		}
		return fn(value)
	case []any:
		return nil, shapeError(at, v, "not one file; declare "+at+"[*]")
	default:
		return nil, shapeError(at, v, "not a file")
	}
}

// shapeError reports that the value at, on the path, is of the wrong shape.
func shapeError(at string, v any, want string) error {
	return fmt.Errorf("%w: %s holds %s, %s", ErrShape, at, kindOf(v), want)
}

// kindOf names a JSON-decoded value's kind.
func kindOf(v any) string {
	switch v.(type) {
	case string:
		return "a string"
	case float64, json.Number:
		return "a number"
	case bool:
		return "a boolean"
	case map[string]any:
		return "an object"
	case []any:
		return "a list"
	default:
		return fmt.Sprintf("a %T", v)
	}
}
