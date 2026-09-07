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
	// reaches its paired BATCH_JOIN, and no edges escape or illegally enter the sub-graph region.
	for splitID, joinID := range pairedJoins {
		if err := validateGatewayRegion(splitID, joinID, GatewayTypeBatchSplit, "BATCH_SPLIT", "BATCH_JOIN", "batch region", pairedJoins, nodesByID, forwardEdges, reverseEdges); err != nil {
			return err
		}
	}

	return nil
}

// validateGatewayRegion checks that the sub-graph between splitID and joinID is strictly
// closed: all paths must terminate at joinID, with no dead ends, escaped edges, or illegal
// entries. Shared by ValidateBatchGateways and ValidateParallelGateways — nestedType and
// pairedJoins scope the "nested split's join must stay inside this region" check to gateways
// of the same kind as splitID itself (a nested BATCH_SPLIT inside a BATCH_SPLIT region, or a
// nested PARALLEL_SPLIT inside a PARALLEL_SPLIT region); it does not check cross-kind nesting.
// splitLabel/joinLabel/regionLabel are purely for error text (e.g. "BATCH_SPLIT"/"BATCH_JOIN"/
// "batch region" vs "PARALLEL_SPLIT"/"PARALLEL_JOIN"/"parallel region") — ValidateBatchGateways
// passes the exact original wording so existing error-message assertions are unaffected.
func validateGatewayRegion(
	splitID, joinID string,
	nestedType GatewayType,
	splitLabel, joinLabel, regionLabel string,
	pairedJoins map[string]string,
	nodesByID map[string]*Node,
	forwardEdges map[string][]Edge,
	reverseEdges map[string][]string,
) error {
	if len(forwardEdges[splitID]) == 0 {
		return fmt.Errorf("%s node %q has no outgoing edges", splitLabel, splitID)
	}

	// 1. Forward reachability: find all nodes in the region, stopping at joinID.
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
			return fmt.Errorf("%s node %q region references non-existent node %q", splitLabel, splitID, nodeID)
		}
		if node.Type == NodeTypeEnd {
			return fmt.Errorf("%s node %q has a path reaching END node %q without passing through paired %s %q", splitLabel, splitID, nodeID, joinLabel, joinID)
		}
		if len(forwardEdges[nodeID]) == 0 {
			return fmt.Errorf("%s node %q region contains dead-end node %q with no outgoing edges", splitLabel, splitID, nodeID)
		}
		if !canReachJoin[nodeID] {
			return fmt.Errorf("%s node %q region contains node %q which cannot reach paired %s %q", splitLabel, splitID, nodeID, joinLabel, joinID)
		}

		// A nested split of the SAME kind must have its paired join contained within this region.
		if node.Type == NodeTypeGateway && node.GatewayType == nestedType && nodeID != splitID {
			nestedJoinID := pairedJoins[nodeID]
			if !regionNodes[nestedJoinID] {
				return fmt.Errorf("%s node %q contains nested %s %q whose paired %s %q is outside the %s", splitLabel, splitID, splitLabel, nodeID, joinLabel, nestedJoinID, regionLabel)
			}
		}

		// Encapsulation: no edges from outside the region may enter intermediate region nodes.
		if nodeID != splitID {
			for _, prev := range reverseEdges[nodeID] {
				if !regionNodes[prev] {
					return fmt.Errorf("%s node %q region contains node %q with incoming edge from outside the %s (from %q)", splitLabel, splitID, nodeID, regionLabel, prev)
				}
			}
		}
	}

	// 4. Encapsulation: all incoming edges to the paired join must originate within this region.
	for _, prev := range reverseEdges[joinID] {
		if !regionNodes[prev] {
			return fmt.Errorf("%s node %q has incoming edge from node %q outside its paired %s %q region", joinLabel, joinID, prev, splitLabel, splitID)
		}
	}

	return nil
}
