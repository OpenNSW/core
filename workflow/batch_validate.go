// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import "fmt"

// ValidateBatchGateways checks structural invariants for BATCH_SPLIT / BATCH_JOIN pairs
// in the workflow definition. Call this at parse-time or at the start of GraphInterpreterWorkflow
// before execution begins.
func ValidateBatchGateways(def WorkflowDefinition) error {
	// Index BATCH_SPLIT and BATCH_JOIN nodes.
	batchSplits := make(map[string]*Node)  // nodeID → node
	batchJoins := make(map[string]*Node)   // nodeID → node
	joinToSplit := make(map[string]string) // joinNodeID → splitNodeID

	for i, node := range def.Nodes {
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

	// Every BATCH_SPLIT must have exactly one paired BATCH_JOIN.
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

	// BATCH_JOIN is a converging gateway; it cannot have more than 1 outgoing edge.
	joinOutEdgeCount := make(map[string]int)
	for _, edge := range def.Edges {
		if _, isJoin := batchJoins[edge.SourceID]; isJoin {
			joinOutEdgeCount[edge.SourceID]++
		}
	}
	for joinID := range batchJoins {
		if count := joinOutEdgeCount[joinID]; count > 1 {
			return fmt.Errorf("BATCH_JOIN node %q cannot have more than 1 outgoing edge, got %d", joinID, count)
		}
	}

	return nil
}
