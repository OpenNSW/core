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
