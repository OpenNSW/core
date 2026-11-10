// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package maputil

import (
	"reflect"
	"testing"
)

func TestGetNestedKey(t *testing.T) {
	m := map[string]any{
		"userform": map[string]any{
			"applicant_name": "Acme",
			"age":            40,
			"address": map[string]any{
				"city": "Sydney",
			},
		},
		"workflow_variables": map[string]any{
			"status": "pending",
		},
	}

	tests := []struct {
		dotPath    string
		expected   any
		expectedOk bool
	}{
		{"userform.applicant_name", "Acme", true},
		{"userform.age", 40, true},
		{"userform.address.city", "Sydney", true},
		{"workflow_variables.status", "pending", true},
		{"nonexistent", nil, false},
		{"userform.nonexistent", nil, false},
		{"userform.address.state", nil, false},
		{"userform.address", map[string]any{"city": "Sydney"}, true},
		{"", nil, false},
	}

	for _, test := range tests {
		got, ok := GetNestedKey(m, test.dotPath)
		if ok != test.expectedOk {
			t.Errorf("GetNestedKey(%q): ok = %v, want %v", test.dotPath, ok, test.expectedOk)
		}
		if ok && !reflect.DeepEqual(got, test.expected) {
			t.Errorf("GetNestedKey(%q): got %v, want %v", test.dotPath, got, test.expected)
		}
	}
}

func TestSetNestedKey(t *testing.T) {
	tests := []struct {
		name     string
		dotPath  string
		value    any
		expected map[string]any
	}{
		{
			name:    "Single level key",
			dotPath: "name",
			value:   "Alice",
			expected: map[string]any{
				"name": "Alice",
			},
		},
		{
			name:    "Two level key",
			dotPath: "userform.applicant_name",
			value:   "Acme",
			expected: map[string]any{
				"userform": map[string]any{
					"applicant_name": "Acme",
				},
			},
		},
		{
			name:    "Three level key",
			dotPath: "userform.address.city",
			value:   "Sydney",
			expected: map[string]any{
				"userform": map[string]any{
					"address": map[string]any{
						"city": "Sydney",
					},
				},
			},
		},
		{
			name:     "Empty path is noop",
			dotPath:  "",
			value:    "ignored",
			expected: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := make(map[string]any)
			SetNestedKey(m, test.dotPath, test.value)

			if test.expected == nil {
				if len(m) > 0 {
					t.Errorf("expected map to be empty, got %v", m)
				}
				return
			}

			if !reflect.DeepEqual(m, test.expected) {
				t.Errorf("got %v, want %v", m, test.expected)
			}
		})
	}
}

func TestSetNestedKey_OverwritesNonMapIntermediate(t *testing.T) {
	m := map[string]any{"user": "not-a-map"}
	SetNestedKey(m, "user.email", "alice@example.com")

	sub, ok := m["user"].(map[string]any)
	if !ok {
		t.Fatal("expected 'user' to be replaced with a map")
	}
	if sub["email"] != "alice@example.com" {
		t.Errorf("expected 'alice@example.com', got %v", sub["email"])
	}
}

func TestSetNestedKey_MergesIntoExistingMap(t *testing.T) {
	m := map[string]any{
		"user": map[string]any{"name": "Bob"},
	}
	SetNestedKey(m, "user.email", "bob@example.com")

	sub := m["user"].(map[string]any)
	if sub["name"] != "Bob" {
		t.Error("existing key 'name' should not be removed")
	}
	if sub["email"] != "bob@example.com" {
		t.Errorf("expected 'bob@example.com', got %v", sub["email"])
	}
}

func TestSetNestedKey_DeepMergeBugFix(t *testing.T) {
	// Bug scenario:
	// 1. set "userinfo.reference_number" to "123"
	// 2. set "userinfo" object to {"first_name": "John"}
	// 3. Expected: userinfo is merged, reference_number is not dropped
	m := map[string]any{}
	SetNestedKey(m, "userinfo.reference_number", "123")
	SetNestedKey(m, "userinfo", map[string]any{"first_name": "John"})

	expected := map[string]any{
		"userinfo": map[string]any{
			"reference_number": "123",
			"first_name":       "John",
		},
	}

	if !reflect.DeepEqual(m, expected) {
		t.Errorf("deep merge failed: got %v, want %v", m, expected)
	}
}

func TestSetNestedKey_DeepCopiesValues(t *testing.T) {
	// Verify that values assigned via SetNestedKey are deep-copied.
	m := map[string]any{}
	sourceMap := map[string]any{
		"inner_key":   "original_val",
		"inner_slice": []any{1, 2},
	}

	SetNestedKey(m, "nested", sourceMap)

	// Mutate source map
	sourceMap["inner_key"] = "mutated_val"
	sourceMap["inner_slice"].([]any)[0] = 99

	destMap := m["nested"].(map[string]any)
	if destMap["inner_key"] != "original_val" {
		t.Error("mutation of sourceMap key affected the destination map")
	}
	if destMap["inner_slice"].([]any)[0] != 1 {
		t.Error("mutation of sourceMap slice element affected the destination map")
	}
}

func TestSetNestedKey_MutationSafety(t *testing.T) {
	sharedMap := map[string]any{"city": "Colombo"}
	m1 := map[string]any{"address": sharedMap}
	m2 := map[string]any{"address": sharedMap}

	SetNestedKey(m1, "address.street", "Main St")

	// Verify that sharedMap was not mutated in-place
	if _, exists := sharedMap["street"]; exists {
		t.Error("sharedMap was mutated in-place")
	}
	// Verify m2 was not mutated
	if _, exists := m2["address"].(map[string]any)["street"]; exists {
		t.Error("m2 was mutated via shared nested map reference")
	}
	// Verify m1 has the updated value
	addr1 := m1["address"].(map[string]any)
	if addr1["street"] != "Main St" || addr1["city"] != "Colombo" {
		t.Errorf("m1 was not updated correctly: %v", m1)
	}
}

func TestDeleteNestedKey(t *testing.T) {
	tests := []struct {
		name     string
		initial  map[string]any
		dotPath  string
		expected map[string]any
	}{
		{
			name:     "top-level key",
			initial:  map[string]any{"a": 1, "b": 2},
			dotPath:  "a",
			expected: map[string]any{"b": 2},
		},
		{
			name: "nested key keeps its siblings",
			initial: map[string]any{
				"userform": map[string]any{"applicant_name": "Acme", "age": 40},
				"other":    "x",
			},
			dotPath: "userform.applicant_name",
			expected: map[string]any{
				"userform": map[string]any{"age": 40},
				"other":    "x",
			},
		},
		{
			name:     "whole subtree",
			initial:  map[string]any{"a": map[string]any{"b": map[string]any{"c": 1}}, "d": 2},
			dotPath:  "a",
			expected: map[string]any{"d": 2},
		},
		{
			name:     "key holding nil",
			initial:  map[string]any{"a": nil, "b": 2},
			dotPath:  "a",
			expected: map[string]any{"b": 2},
		},
		{
			name:     "emptied map stays in place",
			initial:  map[string]any{"a": map[string]any{"b": 1}},
			dotPath:  "a.b",
			expected: map[string]any{"a": map[string]any{}},
		},
		{
			name:     "missing leaf is a no-op",
			initial:  map[string]any{"a": map[string]any{"b": 1}},
			dotPath:  "a.x",
			expected: map[string]any{"a": map[string]any{"b": 1}},
		},
		{
			name:     "missing intermediate creates nothing",
			initial:  map[string]any{"a": 1},
			dotPath:  "x.y.z",
			expected: map[string]any{"a": 1},
		},
		{
			name:     "non-map intermediate is left alone",
			initial:  map[string]any{"a": "scalar"},
			dotPath:  "a.b",
			expected: map[string]any{"a": "scalar"},
		},
		{
			name:     "empty path is a no-op",
			initial:  map[string]any{"a": 1},
			dotPath:  "",
			expected: map[string]any{"a": 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			DeleteNestedKey(test.initial, test.dotPath)
			if !reflect.DeepEqual(test.initial, test.expected) {
				t.Errorf("got %v, want %v", test.initial, test.expected)
			}
		})
	}
}

func TestDeleteNestedKey_NilMap(t *testing.T) {
	DeleteNestedKey(nil, "a.b") // must not panic
}

func TestDeleteNestedKey_MutationSafety(t *testing.T) {
	sharedMap := map[string]any{"city": "Colombo", "street": "Main St"}
	m1 := map[string]any{"address": sharedMap}
	m2 := map[string]any{"address": sharedMap}

	DeleteNestedKey(m1, "address.street")

	if sharedMap["street"] != "Main St" {
		t.Error("sharedMap was mutated in-place")
	}
	if m2["address"].(map[string]any)["street"] != "Main St" {
		t.Error("m2 was mutated via shared nested map reference")
	}
	if want := map[string]any{"address": map[string]any{"city": "Colombo"}}; !reflect.DeepEqual(m1, want) {
		t.Errorf("m1 was not updated correctly: got %v, want %v", m1, want)
	}
}

func TestDeleteNestedKey_MissingPathKeepsMapIdentity(t *testing.T) {
	inner := map[string]any{"b": 1}
	m := map[string]any{"a": inner}

	DeleteNestedKey(m, "a.x")

	// Nothing was removed, so nothing was copied: m still holds the same nested map.
	if reflect.ValueOf(m["a"]).Pointer() != reflect.ValueOf(inner).Pointer() {
		t.Error("a no-op delete replaced the nested map with a copy")
	}
}
