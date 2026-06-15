// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"testing"
)

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
