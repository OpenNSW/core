// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package deepcopy

import "testing"

func TestMap_Nil(t *testing.T) {
	if Map(nil) != nil {
		t.Error("expected deep copy of nil map to be nil")
	}
}

func TestMap_ScalarsAreCopied(t *testing.T) {
	orig := map[string]any{"key1": "value1", "key2": 42}
	copied := Map(orig)

	if len(copied) != len(orig) {
		t.Fatalf("expected length %d, got %d", len(orig), len(copied))
	}
	if copied["key1"] != "value1" || copied["key2"] != 42 {
		t.Error("copied map contents do not match original")
	}

	copied["key1"] = "mutated"
	if orig["key1"] != "value1" {
		t.Error("mutating top-level key of copy affected the original")
	}
}

func TestMap_NestedMapsAreDeepCopied(t *testing.T) {
	orig := map[string]any{
		"address": map[string]any{"city": "Colombo"},
	}
	copied := Map(orig)

	// Mutating a nested map in the copy must not touch the original.
	copied["address"].(map[string]any)["city"] = "mutated"
	if orig["address"].(map[string]any)["city"] != "Colombo" {
		t.Error("mutating nested map of copy affected the original")
	}
}

func TestMap_NestedSlicesAreDeepCopied(t *testing.T) {
	orig := map[string]any{
		"tags":    []any{"a", "b"},
		"records": []any{map[string]any{"n": 1}},
	}
	copied := Map(orig)

	copied["tags"].([]any)[0] = "mutated"
	if orig["tags"].([]any)[0] != "a" {
		t.Error("mutating nested slice element of copy affected the original")
	}

	copied["records"].([]any)[0].(map[string]any)["n"] = 99
	if orig["records"].([]any)[0].(map[string]any)["n"] != 1 {
		t.Error("mutating map inside nested slice of copy affected the original")
	}
}
