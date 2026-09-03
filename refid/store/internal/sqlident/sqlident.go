// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package sqlident validates SQL identifiers (table names) that refid's
// storage backends interpolate directly into raw SQL strings. Table names
// cannot be passed as bind parameters, so every backend must whitelist a
// caller-supplied name before formatting it into a query; sharing one
// validator (instead of one per backend) keeps both backends enforcing the
// identical rule and in lockstep if it's ever hardened.
package sqlident

import (
	"fmt"
	"regexp"
)

var valid = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Validate reports whether name is safe to interpolate directly into SQL as
// an unquoted identifier (e.g. a table name).
func Validate(name string) error {
	if !valid.MatchString(name) {
		return fmt.Errorf("refid: invalid table name %q: must match [a-zA-Z_][a-zA-Z0-9_]*", name)
	}
	return nil
}
