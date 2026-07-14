// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package github

import "testing"

func TestConfigValidateOK(t *testing.T) {
	cfg := Config{Owner: "org", Repo: "config", Ref: "v1.0.0"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateMissingOwner(t *testing.T) {
	cfg := Config{Repo: "config", Ref: "v1.0.0"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing Owner")
	}
}

func TestConfigValidateMissingRepo(t *testing.T) {
	cfg := Config{Owner: "org", Ref: "v1.0.0"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing Repo")
	}
}

func TestConfigValidateMissingRef(t *testing.T) {
	cfg := Config{Owner: "org", Repo: "config"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error for missing Ref")
	}
}
