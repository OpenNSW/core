// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package httputil

import (
	"errors"
	"fmt"
	"regexp"
)

var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]{4}$`)

// Catalog returns every message key of errs, and of ErrInternal, with its
// English default: the starting point for translators. It fails on a code
// declared twice with different content, a key declared twice with a
// different default, a malformed code, a category without a 4xx or 5xx status or a missing key.
//
// A service lists its declared errors once and calls Catalog from a test, to
// catch duplicates, and from a go generate step that writes the result as
// en.json for its frontends.
func Catalog(errs ...Error) (map[string]string, error) {
	catalog := make(map[string]string)
	byCode := make(map[string]Error)
	var problems []error

	addKey := func(code string, m Message, field string, required bool) {
		if m.Key == "" {
			if required || m.DefaultValue != "" {
				problems = append(problems, fmt.Errorf("%s: %s has no key", code, field))
			}
			return
		}
		if prev, ok := catalog[m.Key]; ok && prev != m.DefaultValue {
			problems = append(problems, fmt.Errorf("%s: key %q has default %q, but %q elsewhere", code, m.Key, m.DefaultValue, prev))
			return
		}
		catalog[m.Key] = m.DefaultValue
	}

	for _, e := range append([]Error{ErrInternal}, errs...) {
		if !codePattern.MatchString(e.Code) {
			problems = append(problems, fmt.Errorf("code %q is not an upper-case prefix, a hyphen and 4 digits", e.Code))
		}
		if !validErrorStatus(e.Category.status) {
			problems = append(problems, fmt.Errorf("%s: category %q has status %d, not a 4xx or 5xx", e.Code, e.Category, e.Category.status))
		}
		if prev, ok := byCode[e.Code]; ok {
			if !sameDeclaration(prev, e) {
				problems = append(problems, fmt.Errorf("code %s is declared twice", e.Code))
			}
			continue
		}
		byCode[e.Code] = e
		addKey(e.Code, e.Message, "message", true)
		addKey(e.Code, e.Description, "description", false)
	}

	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return catalog, nil
}

// sameDeclaration reports whether a and b declare the same error, so listing
// one error twice (or listing ErrInternal) is not a conflict.
func sameDeclaration(a, b Error) bool {
	return a.Code == b.Code && a.Category == b.Category &&
		a.Message.Key == b.Message.Key && a.Message.DefaultValue == b.Message.DefaultValue &&
		a.Description.Key == b.Description.Key && a.Description.DefaultValue == b.Description.DefaultValue
}
