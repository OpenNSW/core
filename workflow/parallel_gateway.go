// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"fmt"
	"sort"

	"go.temporal.io/sdk/workflow"

	"github.com/OpenNSW/core/shared/deepcopy"
	"github.com/OpenNSW/core/shared/maputil"
)

// parallelBranch holds the future of a spawned child workflow for one PARALLEL_SPLIT branch.
type parallelBranch struct {
	EdgeID string
	Future workflow.ChildWorkflowFuture
}

// handleParallelSplitGateway runs each matching outgoing edge as its own isolated child
// workflow — a deep copy of the current WorkflowVariables, invisible to sibling branches
// until they all complete — and reconciles their final states back into the parent via the
// paired PARALLEL_JOIN's merge configuration. This mirrors handleBatchSplitGateway's
// spawn-children-then-merge shape, but branches see the FULL variable set (not a partitioned
// subset), so — unlike batch partitions, which are disjoint by construction — more than one
// branch can legitimately touch the same variable, which is exactly what the merge step
// exists to reconcile.
func (g *graphInterpreter) handleParallelSplitGateway(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge) error {
	joinNodeID := findPairedParallelJoin(g.def, node.ID)
	if joinNodeID == "" {
		return fmt.Errorf("PARALLEL_SPLIT node %s: no paired PARALLEL_JOIN found", node.ID)
	}
	joinNode := g.nodes[joinNodeID]
	var mergeByID map[string]string
	if joinNode != nil && joinNode.ParallelJoin != nil {
		mergeByID = joinNode.ParallelJoin.MergeByID
	}

	// 1. Evaluate conditions up front against the pre-split state.
	var matchedEdgeIDs []string
	edgeByID := make(map[string]Edge, len(outEdges))
	for _, e := range outEdges {
		edgeByID[e.ID] = e
		match, err := EvaluateCondition(e.Condition, g.instance.WorkflowVariables)
		if err != nil {
			return err
		}
		if match {
			matchedEdgeIDs = append(matchedEdgeIDs, e.ID)
		}
	}
	if len(matchedEdgeIDs) == 0 {
		nodeInfo.Status = NodeStatusCompleted
		nodeInfo.UpdatedAt = workflow.Now(ctx)
		return g.skipToJoinOutEdge(ctx, joinNodeID)
	}
	// Deterministic branch order: both spawn order (replay-safety) and merge order (so a
	// genuine same-field conflict between branches resolves the same way every run).
	sort.Strings(matchedEdgeIDs)

	// 2. Spawn one isolated child workflow per branch, each a deep copy of the current state.
	baseVars := deepcopy.Map(g.instance.WorkflowVariables)
	parentInfo := workflow.GetInfo(ctx)
	branches := make([]parallelBranch, 0, len(matchedEdgeIDs))
	for _, edgeID := range matchedEdgeIDs {
		e := edgeByID[edgeID]
		subDef := extractSubGraph(g.def, e.TargetID, joinNodeID)
		childVars := deepcopy.Map(baseVars)

		childWorkflowID := FormatBatchChildWorkflowID(parentInfo.WorkflowExecution.ID, node.ID, edgeID)
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: childWorkflowID,
		})
		future := workflow.ExecuteChildWorkflow(childCtx, "GraphInterpreterWorkflow", subDef, childVars)
		branches = append(branches, parallelBranch{EdgeID: edgeID, Future: future})
	}

	// 3. Wait for every branch, then reconcile their final states into one.
	branchVars := make([]map[string]any, 0, len(branches))
	for _, b := range branches {
		var childOutput *WorkflowInstance
		if err := b.Future.Get(ctx, &childOutput); err != nil {
			return fmt.Errorf("PARALLEL_SPLIT node %s: branch %q failed: %w", node.ID, b.EdgeID, err)
		}
		if childOutput == nil {
			return fmt.Errorf("PARALLEL_SPLIT node %s: branch %q returned nil output", node.ID, b.EdgeID)
		}
		branchVars = append(branchVars, childOutput.WorkflowVariables)
	}

	mergedVars, err := mergeParallelBranches(baseVars, branchVars, mergeByID)
	if err != nil {
		return fmt.Errorf("PARALLEL_SPLIT node %s: merge failed: %w", node.ID, err)
	}
	g.instance.WorkflowVariables = mergedVars

	g.instance.AuditTrail = append(g.instance.AuditTrail,
		fmt.Sprintf("PARALLEL_SPLIT %s ran %d branch(es) and merged results", node.ID, len(branches)))

	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)
	return g.skipToJoinOutEdge(ctx, joinNodeID)
}

// handleParallelJoinGateway is a structural passthrough: the paired PARALLEL_SPLIT gateway
// already performed the wait + merge, so the join simply marks itself complete and moves on —
// same shape as handleBatchJoinGateway.
func (g *graphInterpreter) handleParallelJoinGateway(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge) error {
	if node.ParallelJoin == nil || node.ParallelJoin.GatewayNodeID == "" {
		return fmt.Errorf("PARALLEL_JOIN node %s: parallel_join.gateway_node_id is required", node.ID)
	}

	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)

	g.instance.AuditTrail = append(g.instance.AuditTrail,
		fmt.Sprintf("PARALLEL_JOIN %s completed (paired with %s)", node.ID, node.ParallelJoin.GatewayNodeID))

	if len(outEdges) > 0 {
		return g.transitionTo(ctx, outEdges[0])
	}
	return nil
}

// findPairedParallelJoin searches the workflow definition for a PARALLEL_JOIN gateway node
// whose GatewayNodeID matches the given PARALLEL_SPLIT node ID.
func findPairedParallelJoin(def WorkflowDefinition, splitNodeID string) string {
	for _, node := range def.Nodes {
		if node.Type == NodeTypeGateway && node.GatewayType == GatewayTypeParallelJoin &&
			node.ParallelJoin != nil && node.ParallelJoin.GatewayNodeID == splitNodeID {
			return node.ID
		}
	}
	return ""
}

// mergeParallelBranches reconciles N branches' final WorkflowVariables (each a full, isolated
// copy that started as `base`) back into one map. Branches are applied in the order given by
// the caller — expected to be a fixed, deterministic order — so that a genuine same-field
// conflict resolves the same way on every run.
//
// mergeByID-listed variables (top-level variable names only — see ValidateParallelGateways) are
// merged item-by-item, by ID, across every branch: for each item ID, the fields each branch's
// copy of that item carries are unioned into one item. Everything else is merged generically:
// map[string]any values merge key by key (recursively); anything else is last-branch-wins.
func mergeParallelBranches(base map[string]any, branchVars []map[string]any, mergeByID map[string]string) (map[string]any, error) {
	result := deepcopy.Map(base)

	mergeByIDKeys := make(map[string]bool, len(mergeByID))
	for varPath := range mergeByID {
		mergeByIDKeys[varPath] = true
	}

	for varPath, idField := range mergeByID {
		merged, err := mergeItemsByID(result, branchVars, varPath, idField)
		if err != nil {
			return nil, err
		}
		maputil.SetNestedKey(result, varPath, merged)
	}

	for _, bv := range branchVars {
		mergeVariablesInto(result, bv, mergeByIDKeys)
	}

	return result, nil
}

// mergeItemsByID merges a []map[string]any variable (found at varPath) across the base state
// and every branch's copy of it, keyed by idField. For a given item ID, fields present in a
// later branch's copy overwrite the same field from an earlier one (or from base); fields a
// branch's copy simply doesn't have are left untouched. Items are returned in first-seen order
// (base's order, then any items a branch introduced that base didn't have).
func mergeItemsByID(base map[string]any, branchVars []map[string]any, varPath, idField string) ([]any, error) {
	merged := make(map[string]map[string]any)
	var order []string

	absorb := func(raw any, origin string) error {
		if raw == nil {
			return nil
		}
		items, err := toItemSlice(raw)
		if err != nil {
			return fmt.Errorf("variable %q from %s is invalid items: %w", varPath, origin, err)
		}
		for i, item := range items {
			idVal := getItemID(item, idField)
			idStr := fmt.Sprintf("%v", idVal)
			if idVal == nil || idVal == "" || idStr == "" || idStr == "<nil>" {
				return fmt.Errorf("variable %q from %s has item at index %d missing required ID field %q",
					varPath, origin, i, idField)
			}
			existing, ok := merged[idStr]
			if !ok {
				merged[idStr] = deepcopy.Map(item)
				order = append(order, idStr)
				continue
			}
			for k, v := range item {
				existing[k] = deepcopy.Value(v)
			}
		}
		return nil
	}

	if baseRaw, ok := maputil.GetNestedKey(base, varPath); ok {
		if err := absorb(baseRaw, "base state"); err != nil {
			return nil, err
		}
	}
	for i, bv := range branchVars {
		if raw, ok := maputil.GetNestedKey(bv, varPath); ok {
			if err := absorb(raw, fmt.Sprintf("branch %d", i)); err != nil {
				return nil, err
			}
		}
	}

	out := make([]any, 0, len(order))
	for _, id := range order {
		out = append(out, merged[id])
	}
	return out, nil
}

// mergeVariablesInto merges src into dst: map[string]any values merge key by key
// (recursively); anything else (scalars, arrays not handled by mergeItemsByID) is a plain
// overwrite. Top-level keys in skipTopKeys (the mergeByID-configured variables, already
// merged separately) are skipped.
func mergeVariablesInto(dst, src map[string]any, skipTopKeys map[string]bool) {
	for k, v := range src {
		if skipTopKeys[k] {
			continue
		}
		if srcMap, ok := v.(map[string]any); ok {
			if existing, exists := dst[k]; exists {
				if dstMap, ok := existing.(map[string]any); ok {
					mergeVariablesInto(dstMap, srcMap, nil)
					continue
				}
			}
			dst[k] = deepcopy.Map(srcMap)
			continue
		}
		dst[k] = deepcopy.Value(v)
	}
}
