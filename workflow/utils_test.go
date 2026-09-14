// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"fmt"
	"strings"
	"testing"
)

func TestFormatChildWorkflowID(t *testing.T) {
	t.Run("root prefix and length are preserved", func(t *testing.T) {
		tests := []struct {
			name     string
			parentID string
			nodeID   string
			branchID string
			wantRoot string
		}{
			{"simple components", "parent", "node", "branch", "parent"},
			{"parent with hyphens", "consignment-1779417033", "split_task", "customs", "consignment-1779417033"},
			{"parent with multiple hyphens", "my-complex-parent-id-123", "some_node", "some_branch", "my-complex-parent-id-123"},
			{"branch with hyphens", "parent-id-123", "node_id", "oga-phyto", "parent-id-123"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				formatted := FormatChildWorkflowID(tt.parentID, tt.nodeID, tt.branchID)
				root, path := splitRootAndPath(formatted)
				if root != tt.wantRoot {
					t.Errorf("root = %q, want %q", root, tt.wantRoot)
				}
				if len(path) != 16 {
					t.Errorf("path hash length = %d, want 16 hex chars, got %q", len(path), path)
				}
			})
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		a := FormatChildWorkflowID("root", "node", "branch")
		b := FormatChildWorkflowID("root", "node", "branch")
		if a != b {
			t.Errorf("expected deterministic output, got %q and %q", a, b)
		}
	})

	t.Run("different inputs produce different ids", func(t *testing.T) {
		base := FormatChildWorkflowID("root", "node", "branch")
		if other := FormatChildWorkflowID("root", "node", "other-branch"); other == base {
			t.Error("expected different branch ID to change the output")
		}
		if other := FormatChildWorkflowID("root", "other-node", "branch"); other == base {
			t.Error("expected different node ID to change the output")
		}
	})

	t.Run("id length stays bounded across many nested levels", func(t *testing.T) {
		id := "root"
		for i := 0; i < 50; i++ {
			id = FormatChildWorkflowID(id, fmt.Sprintf("node-%d", i), fmt.Sprintf("branch-%d", i))
		}
		wantLen := len("root") + len("--") + 16
		if len(id) != wantLen {
			t.Errorf("id length after 50 levels of nesting = %d, want %d (id: %q)", len(id), wantLen, id)
		}
		if !strings.HasPrefix(id, "root--") {
			t.Errorf("expected id to still be prefixed with the root, got %q", id)
		}
	})

	t.Run("same node/branch pair under different ancestors does not collide", func(t *testing.T) {
		a := FormatChildWorkflowID(FormatChildWorkflowID("root", "a", "1"), "shared-node", "shared-branch")
		b := FormatChildWorkflowID(FormatChildWorkflowID("root", "b", "1"), "shared-node", "shared-branch")
		if a == b {
			t.Error("expected the same nodeID/branchID pair under different ancestor paths to produce different IDs")
		}
	})
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
