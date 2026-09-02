// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package sqlident_test

import (
	"testing"

	"github.com/OpenNSW/core/refid/internal/sqlident"
)

func TestValidate_ValidNames(t *testing.T) {
	valid := []string{"refid_sequences", "custom_sequences", "_leading_underscore", "MixedCase123", "a"}
	for _, name := range valid {
		if err := sqlident.Validate(name); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", name, err)
		}
	}
}

func TestValidate_InvalidNames(t *testing.T) {
	invalid := []string{
		"users; DROP TABLE users;--",
		"refid table",
		"123refid",
		"refid-sequences",
		"table'quote",
		"",
	}
	for _, name := range invalid {
		if err := sqlident.Validate(name); err == nil {
			t.Errorf("expected %q to be rejected, got nil error", name)
		}
	}
}
