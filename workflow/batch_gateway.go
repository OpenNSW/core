// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"fmt"
	"sort"
	"strings"

	"go.temporal.io/sdk/workflow"

	"github.com/OpenNSW/core/shared/maputil"
)

// batchPartition represents a group of items mapped to a specific outgoing edge.
type batchPartition struct {
	Edge  Edge
	Items []map[string]any
}

// batchChild holds the future of a spawned child workflow for a partition edge.
type batchChild struct {
	EdgeID string
	Future workflow.ChildWorkflowFuture
}

// handleBatchSplitGateway partitions the current item slice and spawns one child workflow per
// non-empty partition. Each child runs the same GraphInterpreterWorkflow over a sub-graph
// extracted from the current definition — making the construct composable.
func (g *graphInterpreter) handleBatchSplitGateway(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge) error {
	config := node.BatchGateway
	if config == nil {
		config = &BatchGatewayConfig{}
	}

	itemsVar := config.ItemsVariable
	if itemsVar == "" {
		itemsVar = DefaultItemsVariable
	}
	idField := config.IDField
	if idField == "" {
		idField = DefaultIDField
	}

	// 1. Read items from workflow variables.
	itemsRaw, exists := maputil.GetNestedKey(g.instance.WorkflowVariables, itemsVar)
	if !exists {
		return fmt.Errorf("BATCH_SPLIT node %s: items variable %q not found in workflow variables", node.ID, itemsVar)
	}
	items, err := toItemSlice(itemsRaw)
	if err != nil {
		return fmt.Errorf("BATCH_SPLIT node %s: %w", node.ID, err)
	}

	// 2. Find paired BATCH_JOIN to determine sub-graph boundaries.
	joinNodeID := findPairedBatchJoin(g.def, node.ID)
	if joinNodeID == "" {
		return fmt.Errorf("BATCH_SPLIT node %s: no paired BATCH_JOIN found", node.ID)
	}

	if len(items) == 0 {
		nodeInfo.Status = NodeStatusCompleted
		nodeInfo.UpdatedAt = workflow.Now(ctx)
		return g.skipToJoinOutEdge(ctx, joinNodeID)
	}

	// 3. Validate that each item has a unique, non-empty ID field.
	seenIDs := make(map[string]int, len(items))
	for i, item := range items {
		idVal := getItemID(item, idField)
		idStr := fmt.Sprintf("%v", idVal)
		if idVal == nil || idVal == "" || idStr == "" {
			return fmt.Errorf("BATCH_SPLIT node %s: item at index %d is missing required ID field %q", node.ID, i, idField)
		}
		if firstIdx, exists := seenIDs[idStr]; exists {
			return fmt.Errorf("BATCH_SPLIT node %s: duplicate item ID %q found at index %d (first seen at index %d)", node.ID, idStr, i, firstIdx)
		}
		seenIDs[idStr] = i
	}

	// 4. Runtime depth check via scope path segments.
	scopePath, _ := g.instance.WorkflowVariables[VarScopePath].(string)
	if scopePath == "" {
		scopePath = "root"
	}
	depth := strings.Count(scopePath, "/") / 2
	if depth >= DefaultMaxBatchDepth {
		return fmt.Errorf("BATCH_SPLIT node %s: maximum batch nesting depth %d exceeded (scope_path=%q)", node.ID, DefaultMaxBatchDepth, scopePath)
	}

	// 5. Partition items across outEdges.
	partitions, partitionOrder, err := partitionItems(items, outEdges, g.instance.WorkflowVariables, node.ID, idField)
	if err != nil {
		return err
	}

	// 6. Spawn child workflows per partition.
	children := g.spawnBatchChildren(ctx, partitions, partitionOrder, node.ID, joinNodeID, scopePath, itemsVar)

	// 7. Wait for all child workflows and merge results by item ID.
	mergedItems, err := collectAndMergeBatchResults(ctx, children, items, itemsVar, idField, node.ID)
	if err != nil {
		return err
	}
	maputil.SetNestedKey(g.instance.WorkflowVariables, itemsVar, mergedItems)

	g.instance.AuditTrail = append(g.instance.AuditTrail,
		fmt.Sprintf("BATCH_SPLIT %s partitioned %d items into %d partitions", node.ID, len(items), len(partitions)))

	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)
	return g.skipToJoinOutEdge(ctx, joinNodeID)
}

// partitionItems evaluates each edge condition per-item with item + workflow variables in scope.
// First matching edge wins. Returns partitions map, ordered edge IDs, or error.
func partitionItems(
	items []map[string]any,
	outEdges []Edge,
	workflowVars map[string]any,
	nodeID, idField string,
) (map[string]*batchPartition, []string, error) {
	partitionByEdge := make(map[string]*batchPartition)
	var partitionOrder []string
	var unmatchedItems []string

	for _, item := range items {
		evalScope := make(map[string]any, len(workflowVars)+1)
		for k, v := range workflowVars {
			evalScope[k] = v
		}
		evalScope["item"] = item

		matched := false
		var defaultEdge *Edge

		for i, e := range outEdges {
			if e.Condition == "" || e.Condition == "true" {
				if defaultEdge == nil {
					edgeCopy := outEdges[i]
					defaultEdge = &edgeCopy
				}
				continue
			}
			match, evalErr := EvaluateCondition(e.Condition, evalScope)
			if evalErr != nil {
				return nil, nil, fmt.Errorf("BATCH_SPLIT node %s: edge %s condition error for item %v: %w",
					nodeID, e.ID, getItemID(item, idField), evalErr)
			}
			if match {
				p, exists := partitionByEdge[e.ID]
				if !exists {
					p = &batchPartition{Edge: e}
					partitionByEdge[e.ID] = p
					partitionOrder = append(partitionOrder, e.ID)
				}
				p.Items = append(p.Items, item)
				matched = true
				break // first match wins
			}
		}

		if !matched {
			if defaultEdge != nil {
				p, exists := partitionByEdge[defaultEdge.ID]
				if !exists {
					p = &batchPartition{Edge: *defaultEdge}
					partitionByEdge[defaultEdge.ID] = p
					partitionOrder = append(partitionOrder, defaultEdge.ID)
				}
				p.Items = append(p.Items, item)
			} else {
				unmatchedItems = append(unmatchedItems, fmt.Sprintf("%v", getItemID(item, idField)))
			}
		}
	}

	if len(unmatchedItems) > 0 {
		return nil, nil, fmt.Errorf("BATCH_SPLIT node %s: items unmatched by any edge condition and no default branch: %s",
			nodeID, strings.Join(unmatchedItems, ", "))
	}

	if len(partitionByEdge) > DefaultMaxChildrenPerGateway {
		return nil, nil, fmt.Errorf("BATCH_SPLIT node %s: partition count %d exceeds maximum %d",
			nodeID, len(partitionByEdge), DefaultMaxChildrenPerGateway)
	}

	return partitionByEdge, partitionOrder, nil
}

// spawnBatchChildren executes a child workflow for each non-empty partition in deterministic order.
func (g *graphInterpreter) spawnBatchChildren(
	ctx workflow.Context,
	partitions map[string]*batchPartition,
	partitionOrder []string,
	nodeID, joinNodeID, scopePath, itemsVar string,
) []batchChild {
	parentInfo := workflow.GetInfo(ctx)
	var children []batchChild

	sort.Strings(partitionOrder) // deterministic for replay
	for _, edgeID := range partitionOrder {
		p := partitions[edgeID]
		if len(p.Items) == 0 {
			continue
		}

		subDef := extractSubGraph(g.def, p.Edge.TargetID, joinNodeID)
		childScopePath := scopePath + "/" + nodeID + "/" + edgeID

		childVars := make(map[string]any, len(g.instance.WorkflowVariables)+2)
		for k, v := range g.instance.WorkflowVariables {
			childVars[k] = v
		}
		maputil.SetNestedKey(childVars, itemsVar, toAnySlice(p.Items))
		childVars[VarScopePath] = childScopePath

		childWorkflowID := FormatBatchChildWorkflowID(parentInfo.WorkflowExecution.ID, nodeID, edgeID)
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: childWorkflowID,
		})

		future := workflow.ExecuteChildWorkflow(childCtx, "GraphInterpreterWorkflow", subDef, childVars)
		children = append(children, batchChild{EdgeID: edgeID, Future: future})
	}

	return children
}

// collectAndMergeBatchResults awaits all child workflows and merges their item mutations back by ID.
func collectAndMergeBatchResults(
	ctx workflow.Context,
	children []batchChild,
	originalItems []map[string]any,
	itemsVar, idField, nodeID string,
) ([]any, error) {
	mergedItems := make(map[string]map[string]any)

	for _, child := range children {
		var childOutput *WorkflowInstance
		if err := child.Future.Get(ctx, &childOutput); err != nil {
			return nil, fmt.Errorf("BATCH_SPLIT node %s: child workflow for partition edge %q failed: %w",
				nodeID, child.EdgeID, err)
		}
		if childOutput == nil {
			return nil, fmt.Errorf("BATCH_SPLIT node %s: child workflow for partition edge %q returned nil output",
				nodeID, child.EdgeID)
		}

		childItemsRaw, exists := maputil.GetNestedKey(childOutput.WorkflowVariables, itemsVar)
		if !exists {
			return nil, fmt.Errorf("BATCH_SPLIT node %s: child workflow for partition edge %q missing items variable %q in output",
				nodeID, child.EdgeID, itemsVar)
		}
		childSlice, err := toItemSlice(childItemsRaw)
		if err != nil {
			return nil, fmt.Errorf("BATCH_SPLIT node %s: child workflow for partition edge %q returned invalid items: %w",
				nodeID, child.EdgeID, err)
		}
		for _, item := range childSlice {
			idVal := getItemID(item, idField)
			idStr := fmt.Sprintf("%v", idVal)
			if idVal == nil || idVal == "" || idStr == "" {
				return nil, fmt.Errorf("BATCH_SPLIT node %s: child workflow for partition edge %q returned item missing required ID field %q",
					nodeID, child.EdgeID, idField)
			}
			if _, alreadySeen := mergedItems[idStr]; alreadySeen {
				return nil, fmt.Errorf("BATCH_SPLIT node %s: duplicate item ID %q returned across child partitions (from edge %q)",
					nodeID, idStr, child.EdgeID)
			}
			mergedItems[idStr] = item
		}
	}

	result := make([]any, 0, len(originalItems))
	for _, originalItem := range originalItems {
		id := fmt.Sprintf("%v", getItemID(originalItem, idField))
		if merged, ok := mergedItems[id]; ok {
			result = append(result, merged)
		} else {
			result = append(result, originalItem)
		}
	}

	return result, nil
}

// handleBatchJoinGateway is a structural passthrough. The paired BATCH_SPLIT gateway already
// performed the wait + merge, so the join simply marks itself complete and transitions onward.
func (g *graphInterpreter) handleBatchJoinGateway(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge) error {
	config := node.BatchJoin
	if config == nil || config.GatewayNodeID == "" {
		return fmt.Errorf("BATCH_JOIN node %s: batch_join.gateway_node_id is required", node.ID)
	}

	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)

	g.instance.AuditTrail = append(g.instance.AuditTrail,
		fmt.Sprintf("BATCH_JOIN %s completed (paired with %s)", node.ID, config.GatewayNodeID))

	if len(outEdges) > 0 {
		return g.transitionTo(ctx, outEdges[0])
	}
	return nil
}

// skipToJoinOutEdge transitions the interpreter to the first outgoing edge of the specified
// join node, skipping the join node's own handler (since BATCH_SPLIT already did the work).
func (g *graphInterpreter) skipToJoinOutEdge(ctx workflow.Context, joinNodeID string) error {
	// Mark the join node as completed.
	joinInfo := g.instance.NodeInfo[joinNodeID]
	if joinInfo != nil {
		joinInfo.Status = NodeStatusCompleted
		joinInfo.UpdatedAt = workflow.Now(ctx)
	}

	joinOutEdges := g.outEdges[joinNodeID]
	if len(joinOutEdges) > 0 {
		return g.transitionTo(ctx, joinOutEdges[0])
	}
	return nil
}

// --- Helpers ---

// toItemSlice converts a raw interface value to a slice of map[string]any items.
func toItemSlice(raw any) ([]map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case []map[string]any:
		return v, nil
	case []any:
		result := make([]map[string]any, len(v))
		for i, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("item at index %d is not a map[string]any: %T", i, item)
			}
			result[i] = m
		}
		return result, nil
	default:
		return nil, fmt.Errorf("items variable is not a slice type: %T", raw)
	}
}

// toAnySlice converts []map[string]any to []any for storage in workflow variables.
func toAnySlice(items []map[string]any) []any {
	result := make([]any, len(items))
	for i, item := range items {
		result[i] = item
	}
	return result
}

// getItemID extracts the ID field value from an item map.
func getItemID(item map[string]any, idField string) any {
	if id, ok := item[idField]; ok {
		return id
	}
	return ""
}

// findPairedBatchJoin searches the workflow definition for a BATCH_JOIN gateway node
// whose GatewayNodeID matches the given BATCH_SPLIT node ID.
func findPairedBatchJoin(def WorkflowDefinition, splitNodeID string) string {
	for _, node := range def.Nodes {
		if node.Type == NodeTypeGateway && node.GatewayType == GatewayTypeBatchJoin &&
			node.BatchJoin != nil && node.BatchJoin.GatewayNodeID == splitNodeID {
			return node.ID
		}
	}
	return ""
}

// extractSubGraph extracts a sub-graph from the definition, containing all nodes and edges
// reachable from startNodeID up to (but not including) stopNodeID. The extracted graph gets
// synthetic START and END nodes wrapping the sub-graph so it's a valid WorkflowDefinition.
func extractSubGraph(def WorkflowDefinition, startNodeID, stopNodeID string) WorkflowDefinition {
	// Collect all reachable node IDs from startNodeID, stopping at stopNodeID.
	reachable := make(map[string]bool)
	var walk func(nodeID string)
	walk = func(nodeID string) {
		if nodeID == stopNodeID || reachable[nodeID] {
			return
		}
		reachable[nodeID] = true
		for _, e := range def.Edges {
			if e.SourceID == nodeID {
				walk(e.TargetID)
			}
		}
	}
	walk(startNodeID)

	// If the branch connects directly to the join, synthetic START connects directly to synthetic END.
	entryTargetID := startNodeID
	if entryTargetID == stopNodeID {
		entryTargetID = "_batch_end"
	}

	subNodes := []Node{
		{ID: "_batch_start", Type: NodeTypeStart},
	}
	subEdges := []Edge{
		{ID: "_batch_e_start", SourceID: "_batch_start", TargetID: entryTargetID},
	}

	for _, node := range def.Nodes {
		if reachable[node.ID] {
			subNodes = append(subNodes, node)
		}
	}

	endNode := Node{ID: "_batch_end", Type: NodeTypeEnd}
	subNodes = append(subNodes, endNode)

	for _, edge := range def.Edges {
		if !reachable[edge.SourceID] {
			continue
		}
		if edge.TargetID == stopNodeID {
			// Redirect to synthetic END.
			subEdges = append(subEdges, Edge{
				ID:        edge.ID + "_to_end",
				SourceID:  edge.SourceID,
				TargetID:  "_batch_end",
				Condition: edge.Condition,
			})
		} else if reachable[edge.TargetID] {
			subEdges = append(subEdges, edge)
		}
	}

	return WorkflowDefinition{
		ID:    def.ID + "_batch_" + startNodeID,
		Name:  def.Name + " (batch sub-graph)",
		Nodes: subNodes,
		Edges: subEdges,
	}
}
