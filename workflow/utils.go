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

// FormatChildWorkflowID constructs a deterministic child workflow ID from the parent's workflow
// ID, the gateway node ID, and the branch ID.
//
// The result is always "<root>--<16-hex-char hash>": each level's hash folds in the parent's own
// path hash, not just this level's nodeID/branchID, so the ID uniquely commits to the full
// ancestor chain no matter how deep the nesting goes. Fixed-width output keeps the ID within
// Temporal's varchar(255) current_executions.workflow_id column regardless of branch depth.
//
// The root segment stays an unmangled, "--"-delimited prefix so it remains identifiable in the
// database (e.g. for querying current_executions by consignment). Code that needs the root
// programmatically should read VarRootWorkflowID, which the engine propagates explicitly, rather
// than parsing this ID.
func FormatChildWorkflowID(parentWorkflowID, nodeID, branchID string) string {
	root, parentPath := splitRootAndPath(parentWorkflowID)
	sum := sha256.Sum256([]byte(parentPath + "|" + nodeID + "|" + branchID))
	return root + "--" + hex.EncodeToString(sum[:8])
}

// splitRootAndPath splits a workflow ID into its root segment (everything before the first "--")
// and the path hash that follows it. An ID with no "--" is itself the root, with an empty path.
func splitRootAndPath(workflowID string) (root, path string) {
	if idx := strings.Index(workflowID, "--"); idx != -1 {
		return workflowID[:idx], workflowID[idx+2:]
	}
	return workflowID, ""
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

// FormatBatchChildWorkflowID constructs a deterministic child workflow ID for batch partitions
// and PARALLEL_SPLIT branches. See FormatChildWorkflowID: the hash chain folds in full ancestry,
// which is what prevents collisions when the same partition key appears at different gateway
// levels.
func FormatBatchChildWorkflowID(parentWorkflowID, nodeID, partitionKey string) string {
	return FormatChildWorkflowID(parentWorkflowID, nodeID, partitionKey)
}
