// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tlsguard

import "testing"

func TestIsDevEnvironment(t *testing.T) {
	cases := map[string]bool{
		"development":   true,
		"Development":   true,
		" development ": true,
		"DEVELOPMENT":   true,
		"production":    false,
		"dev":           false,
		"":              false,
		"staging":       false,
	}
	for val, want := range cases {
		t.Run(val, func(t *testing.T) {
			t.Setenv(EnvKey, val)
			if got := IsDevEnvironment(); got != want {
				t.Fatalf("IsDevEnvironment() with %s=%q = %v, want %v", EnvKey, val, got, want)
			}
		})
	}
}

func TestGuard_AllowsInDevelopment(t *testing.T) {
	t.Setenv(EnvKey, "development")
	if err := Guard("test"); err != nil {
		t.Fatalf("expected nil in development, got %v", err)
	}
}

func TestGuard_FailsClosedOutsideDevelopment(t *testing.T) {
	for _, val := range []string{"", "production", "staging"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv(EnvKey, val)
			err := Guard("AUTH_JWKS_INSECURE_SKIP_VERIFY")
			if err == nil {
				t.Fatalf("expected error when %s=%q, got nil", EnvKey, val)
			}
		})
	}
}
