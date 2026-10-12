// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// parseMappingKey strips a trailing "?" from a mapping key and reports whether
// the entry should be treated as optional. A trailing "?" marks the entry as
// optional, meaning the engine will skip the mapping silently when the source
// key is absent instead of failing the workflow.
func parseMappingKey(rawKey string) (key string, optional bool) {
	if strings.HasSuffix(rawKey, "?") {
		return rawKey[:len(rawKey)-1], true
	}
	return rawKey, false
}

// describeVariables renders keys for a "required ... not found" message: "variable 'a'" for one,
// "variables 'a', 'b'" for several.
func describeVariables(keys []string) string {
	noun := "variable"
	if len(keys) > 1 {
		noun = "variables"
	}
	return noun + " " + quoteKeys(keys)
}

// quoteKeys renders keys as 'a', 'b' for error messages.
func quoteKeys(keys []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = "'" + k + "'"
	}
	return strings.Join(quoted, ", ")
}

const (
	// maxWorkflowIDLen mirrors Temporal's Postgres schema limit on
	// current_executions.workflow_id (varchar(255)).
	maxWorkflowIDLen = 255
	// childIDSuffixLen is the fixed width of the "--<16-hex-char hash>" suffix
	// FormatChildWorkflowID appends to the case ID.
	childIDSuffixLen = 2 + 16
	// maxPrefixForChildID is the longest case ID FormatChildWorkflowID can prefix without the
	// result exceeding maxWorkflowIDLen.
	maxPrefixForChildID = maxWorkflowIDLen - childIDSuffixLen
)

// FormatChildWorkflowID constructs a deterministic child workflow ID from the case ID,
// the parent's own workflow ID, the gateway node ID, and the branch ID.
//
// The result is always "<root>--<16-hex-char hash>": each level's hash folds in the parent's full
// workflow ID, not just this level's nodeID/branchID, so the ID uniquely commits to the full
// ancestor chain no matter how deep the nesting goes. Fixed-width output keeps the ID within
// Temporal's varchar(255) current_executions.workflow_id column regardless of branch depth — an
// oversized case ID is capped via boundPrefixForChildID first, so the guarantee holds independent
// of how long the caller's case ID is. Two workflows of the same case (a host's several
// top-level workflows) share the prefix; the hash, which folds in the parent's workflow ID, keeps
// their children apart.
//
// caseID is taken as given rather than parsed out of parentWorkflowID — callers already have it
// via VarCaseID, which the engine propagates explicitly to every level of nesting.
func FormatChildWorkflowID(caseID, parentWorkflowID, nodeID, branchID string) string {
	root := boundPrefixForChildID(caseID)
	sum := sha256.Sum256([]byte(parentWorkflowID + "|" + nodeID + "|" + branchID))
	return root + "--" + hex.EncodeToString(sum[:8])
}

// boundPrefixForChildID caps a case ID at maxPrefixForChildID so FormatChildWorkflowID's
// output can never exceed maxWorkflowIDLen. A root within budget passes through unchanged; an
// oversized one is truncated and given a short hash suffix of the full root, so two long roots
// that happen to share a prefix still can't collide once capped.
func boundPrefixForChildID(root string) string {
	if len(root) <= maxPrefixForChildID {
		return root
	}
	sum := sha256.Sum256([]byte(root))
	suffix := hex.EncodeToString(sum[:4]) // 8 hex chars
	keep := maxPrefixForChildID - len(suffix) - 1
	if keep < 0 {
		keep = 0
	}
	return root[:keep] + "~" + suffix
}

// ParseSplitTaskItem parses a raw interface item into a SplitTaskItem.
func ParseSplitTaskItem(itemRaw any) (SplitTaskItem, error) {
	if item, ok := itemRaw.(SplitTaskItem); ok {
		return item, nil
	}
	if itemPtr, ok := itemRaw.(*SplitTaskItem); ok && itemPtr != nil {
		return *itemPtr, nil
	}

	m, ok := itemRaw.(map[string]any)
	if !ok {
		return SplitTaskItem{}, fmt.Errorf("item is not a map[string]any: %T", itemRaw)
	}

	var item SplitTaskItem
	if val, exists := m["template_id"]; exists {
		if strVal, ok := val.(string); ok {
			item.TemplateID = strVal
		}
	}
	if val, exists := m["branch_id"]; exists {
		if strVal, ok := val.(string); ok {
			item.BranchID = strVal
		}
	}
	if val, exists := m["payload"]; exists {
		if mapVal, ok := val.(map[string]any); ok {
			item.Payload = mapVal
		}
	}

	return item, nil
}
