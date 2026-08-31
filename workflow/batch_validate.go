// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import "fmt"

// ValidateBatchGateways checks structural invariants and topological containment for
// BATCH_SPLIT / BATCH_JOIN pairs in the workflow definition. Call this at parse-time or
// at the start of GraphInterpreterWorkflow before execution begins.
func ValidateBatchGateways(def WorkflowDefinition) error {
	// Index all nodes by ID for fast lookup.
	nodesByID := make(map[string]*Node, len(def.Nodes))
	batchSplits := make(map[string]*Node)  // nodeID → node
	batchJoins := make(map[string]*Node)   // nodeID → node
	joinToSplit := make(map[string]string) // joinNodeID → splitNodeID

	for i, node := range def.Nodes {
		nodesByID[node.ID] = &def.Nodes[i]
		if node.Type != NodeTypeGateway {
			continue
		}
		switch node.GatewayType {
		case GatewayTypeBatchSplit:
			batchSplits[node.ID] = &def.Nodes[i]
		case GatewayTypeBatchJoin:
			if node.BatchJoin == nil || node.BatchJoin.GatewayNodeID == "" {
				return fmt.Errorf("BATCH_JOIN node %q: batch_join.gateway_node_id is required", node.ID)
			}
			batchJoins[node.ID] = &def.Nodes[i]
			joinToSplit[node.ID] = node.BatchJoin.GatewayNodeID
		}
	}

	// 1. Every BATCH_SPLIT must have exactly one paired BATCH_JOIN.
	pairedJoins := make(map[string]string) // splitNodeID → joinNodeID
	for joinID, splitID := range joinToSplit {
		if _, exists := batchSplits[splitID]; !exists {
			return fmt.Errorf("BATCH_JOIN node %q references non-existent BATCH_SPLIT node %q", joinID, splitID)
		}
		if existingJoin, alreadyPaired := pairedJoins[splitID]; alreadyPaired {
			return fmt.Errorf("BATCH_SPLIT node %q has multiple BATCH_JOIN nodes: %q and %q", splitID, existingJoin, joinID)
		}
		pairedJoins[splitID] = joinID
	}

	for splitID := range batchSplits {
		if _, paired := pairedJoins[splitID]; !paired {
			return fmt.Errorf("BATCH_SPLIT node %q has no paired BATCH_JOIN", splitID)
		}
	}

	// 2. Build forward and reverse edge mappings.
	forwardEdges := make(map[string][]Edge)
	reverseEdges := make(map[string][]string)
	for _, edge := range def.Edges {
		forwardEdges[edge.SourceID] = append(forwardEdges[edge.SourceID], edge)
		reverseEdges[edge.TargetID] = append(reverseEdges[edge.TargetID], edge.SourceID)
	}

	// 3. BATCH_JOIN is a converging gateway; it cannot have more than 1 outgoing edge.
	for joinID := range batchJoins {
		if count := len(forwardEdges[joinID]); count > 1 {
			return fmt.Errorf("BATCH_JOIN node %q cannot have more than 1 outgoing edge, got %d", joinID, count)
		}
	}

	// 4. Sub-graph topological containment: enforce that every path from BATCH_SPLIT
	// reaches its paired BATCH_JOIN, and no edges escape the sub-graph region.
	for splitID, joinID := range pairedJoins {
		if err := validateBatchRegion(splitID, joinID, nodesByID, forwardEdges, reverseEdges); err != nil {
			return err
		}
	}

	return nil
}

// validateBatchRegion checks that the sub-graph between splitID and joinID is strictly
// closed: all paths must terminate at joinID, with no dead ends or escaped edges.
func validateBatchRegion(
	splitID, joinID string,
	nodesByID map[string]*Node,
	forwardEdges map[string][]Edge,
	reverseEdges map[string][]string,
) error {
	if len(forwardEdges[splitID]) == 0 {
		return fmt.Errorf("BATCH_SPLIT node %q has no outgoing edges", splitID)
	}

	// 1. Forward reachability: find all nodes in the batch region, stopping at joinID.
	regionNodes := make(map[string]bool)
	queue := []string{splitID}
	regionNodes[splitID] = true

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, edge := range forwardEdges[curr] {
			target := edge.TargetID
			if target == joinID {
				// Stop forward expansion at the paired join.
				continue
			}
			if !regionNodes[target] {
				regionNodes[target] = true
				queue = append(queue, target)
			}
		}
	}

	// 2. Backward reachability: find all nodes that can reach joinID.
	canReachJoin := make(map[string]bool)
	revQueue := []string{joinID}
	canReachJoin[joinID] = true

	for len(revQueue) > 0 {
		curr := revQueue[0]
		revQueue = revQueue[1:]

		for _, prev := range reverseEdges[curr] {
			if !canReachJoin[prev] {
				canReachJoin[prev] = true
				revQueue = append(revQueue, prev)
			}
		}
	}

	// 3. Validate every node in the sub-graph region.
	for nodeID := range regionNodes {
		node, exists := nodesByID[nodeID]
		if !exists {
			return fmt.Errorf("BATCH_SPLIT node %q region references non-existent node %q", splitID, nodeID)
		}
		if node.Type == NodeTypeEnd {
			return fmt.Errorf("BATCH_SPLIT node %q has a path reaching END node %q without passing through paired BATCH_JOIN %q", splitID, nodeID, joinID)
		}
		if len(forwardEdges[nodeID]) == 0 {
			return fmt.Errorf("BATCH_SPLIT node %q region contains dead-end node %q with no outgoing edges", splitID, nodeID)
		}
		if !canReachJoin[nodeID] {
			return fmt.Errorf("BATCH_SPLIT node %q region contains node %q which cannot reach paired BATCH_JOIN %q", splitID, nodeID, joinID)
		}
	}

	return nil
}
