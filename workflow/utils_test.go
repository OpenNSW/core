package engine

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
		got, ok := getNestedKey(m, test.dotPath)
		if ok != test.expectedOk {
			t.Errorf("getNestedKey(%q): ok = %v, want %v", test.dotPath, ok, test.expectedOk)
		}
		// Bug fix: use reflect.DeepEqual instead of != so that map values
		// (like the "userform.address" case) are compared correctly.
		if ok && !reflect.DeepEqual(got, test.expected) {
			t.Errorf("getNestedKey(%q): got %v, want %v", test.dotPath, got, test.expected)
		}
	}
}

func TestSetNestedKey(t *testing.T) {
	tests := []struct {
		dotPath  string
		value    any
		expected map[string]any
	}{
		{
			dotPath: "userform.applicant_name",
			value:   "Acme",
			expected: map[string]any{
				"userform": map[string]any{
					"applicant_name": "Acme",
				},
			},
		},
		{
			dotPath: "userform.age",
			value:   40,
			expected: map[string]any{
				"userform": map[string]any{
					"age": 40,
				},
			},
		},
		{
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
			dotPath: "workflow_variables.status",
			value:   "pending",
			expected: map[string]any{
				"workflow_variables": map[string]any{
					"status": "pending",
				},
			},
		},
		{
			dotPath:  "",
			value:    "ignored",
			expected: nil,
		},
	}

	for _, test := range tests {
		m := make(map[string]any)
		setNestedKey(m, test.dotPath, test.value)

		if test.expected == nil {
			if len(m) > 0 {
				t.Errorf("setNestedKey(%q): map not empty", test.dotPath)
			}
			continue
		}

		if len(m) != len(test.expected) {
			t.Errorf("setNestedKey(%q): map lengths differ: got %d, want %d",
				test.dotPath, len(m), len(test.expected))
		}

		for k, v := range test.expected {
			if got, ok := m[k]; !ok {
				t.Errorf("setNestedKey(%q): key %q not found", test.dotPath, k)
			} else if !reflect.DeepEqual(got, v) {
				t.Errorf("setNestedKey(%q): value for key %q: got %v, want %v",
					test.dotPath, k, got, v)
			}
		}
	}

	// test nested overwrite
	m := map[string]any{
		"userform": map[string]any{
			"applicant_name": "Original Name",
			"address": map[string]any{
				"city":  "Old City",
				"state": "Old State",
			},
		},
	}
	setNestedKey(m, "userform.applicant_name", "New Name")
	setNestedKey(m, "userform.address.state", "New State")

	expected := map[string]any{
		"userform": map[string]any{
			"applicant_name": "New Name",
			"address": map[string]any{
				"city":  "Old City",
				"state": "New State",
			},
		},
	}

	if !equalMaps(m, expected) {
		t.Errorf("setNestedKey nested overwrite: got %v, want %v", m, expected)
	}
}

func TestParseMappingKey(t *testing.T) {
	tests := []struct {
		raw         string
		expectedKey string
		expectedOpt bool
	}{
		{"global_user_email", "global_user_email", false},
		{"global_user_phone?", "global_user_phone", true},
		{"user.phone?", "user.phone", true},
		{"user.phone", "user.phone", false},
		{"?", "", true},
		{"", "", false},
	}

	for _, test := range tests {
		gotKey, gotOpt := parseMappingKey(test.raw)
		if gotKey != test.expectedKey || gotOpt != test.expectedOpt {
			t.Errorf("parseMappingKey(%q): got (%q, %v), want (%q, %v)",
				test.raw, gotKey, gotOpt, test.expectedKey, test.expectedOpt)
		}
	}
}

// helper for deep map equality
func equalMaps(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		if !equalValues(v, vb) {
			return false
		}
	}
	return true
}

// helper for deep value equality
func equalValues(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	// try string
	sa, saOk := a.(string)
	sb, sbOk := b.(string)
	if saOk && sbOk && sa == sb {
		return true
	}
	// try int
	ia, iaOk := a.(int)
	ib, ibOk := b.(int)
	if iaOk && ibOk && ia == ib {
		return true
	}
	// try float64
	fa, faOk := a.(float64)
	fb, fbOk := b.(float64)
	if faOk && fbOk && fa == fb {
		return true
	}
	return false
}

func TestFormatChildWorkflowID(t *testing.T) {
	tests := []struct {
		name       string
		parentID   string
		nodeID     string
		branchID   string
		expectedID string
	}{
		{
			name:       "simple components",
			parentID:   "parent",
			nodeID:     "node",
			branchID:   "branch",
			expectedID: "parent--node--branch",
		},
		{
			name:       "parent with hyphens",
			parentID:   "consignment-1779417033",
			nodeID:     "split_task",
			branchID:   "customs",
			expectedID: "consignment-1779417033--split_task--customs",
		},
		{
			name:       "parent with multiple hyphens",
			parentID:   "my-complex-parent-id-123",
			nodeID:     "some_node",
			branchID:   "some_branch",
			expectedID: "my-complex-parent-id-123--some_node--some_branch",
		},
		{
			name:       "branch with hyphens",
			parentID:   "parent-id-123",
			nodeID:     "node_id",
			branchID:   "oga-phyto",
			expectedID: "parent-id-123--node_id--oga-phyto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatted := FormatChildWorkflowID(tt.parentID, tt.nodeID, tt.branchID)
			if formatted != tt.expectedID {
				t.Errorf("FormatChildWorkflowID() = %q, want %q", formatted, tt.expectedID)
			}
		})
	}
}
