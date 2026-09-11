// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"fmt"
	"strings"
)

// ValidateParallelGateways checks structural invariants and topological containment for
// PARALLEL_SPLIT / PARALLEL_JOIN pairs in the workflow definition, mirroring
// ValidateBatchGateways for BATCH_SPLIT / BATCH_JOIN. Call this at parse-time or at the start
// of GraphInterpreterWorkflow before execution begins.
func ValidateParallelGateways(def WorkflowDefinition) error {
	nodesByID := make(map[string]*Node, len(def.Nodes))
	parallelSplits := make(map[string]*Node)
	parallelJoins := make(map[string]*Node)
	joinToSplit := make(map[string]string)

	for i, node := range def.Nodes {
		nodesByID[node.ID] = &def.Nodes[i]
		if node.Type != NodeTypeGateway {
			continue
		}
		switch node.GatewayType {
		case GatewayTypeParallelSplit:
			parallelSplits[node.ID] = &def.Nodes[i]
		case GatewayTypeParallelJoin:
			if node.ParallelJoin == nil || node.ParallelJoin.GatewayNodeID == "" {
				return fmt.Errorf("PARALLEL_JOIN node %q: parallel_join.gateway_node_id is required", node.ID)
			}
			for varPath, idField := range node.ParallelJoin.MergeByID {
				if strings.Contains(varPath, ".") {
					return fmt.Errorf("PARALLEL_JOIN node %q: merge_by_id key %q must be a top-level workflow variable (nested dot-paths are not supported)", node.ID, varPath)
				}
				if idField == "" {
					return fmt.Errorf("PARALLEL_JOIN node %q: merge_by_id key %q has empty ID field", node.ID, varPath)
				}
			}
			parallelJoins[node.ID] = &def.Nodes[i]
			joinToSplit[node.ID] = node.ParallelJoin.GatewayNodeID
		}
	}

	// 1. Every PARALLEL_SPLIT must have exactly one paired PARALLEL_JOIN.
	pairedJoins := make(map[string]string) // splitNodeID -> joinNodeID
	for joinID, splitID := range joinToSplit {
		if _, exists := parallelSplits[splitID]; !exists {
			return fmt.Errorf("PARALLEL_JOIN node %q references non-existent PARALLEL_SPLIT node %q", joinID, splitID)
		}
		if existingJoin, alreadyPaired := pairedJoins[splitID]; alreadyPaired {
			return fmt.Errorf("PARALLEL_SPLIT node %q has multiple PARALLEL_JOIN nodes: %q and %q", splitID, existingJoin, joinID)
		}
		pairedJoins[splitID] = joinID
	}

	for splitID := range parallelSplits {
		if _, paired := pairedJoins[splitID]; !paired {
			return fmt.Errorf("PARALLEL_SPLIT node %q has no paired PARALLEL_JOIN", splitID)
		}
	}

	// 2. Build forward and reverse edge mappings.
	forwardEdges := make(map[string][]Edge)
	reverseEdges := make(map[string][]string)
	for _, edge := range def.Edges {
		forwardEdges[edge.SourceID] = append(forwardEdges[edge.SourceID], edge)
		reverseEdges[edge.TargetID] = append(reverseEdges[edge.TargetID], edge.SourceID)
	}

	// 3. PARALLEL_JOIN is a converging gateway; it cannot have more than 1 outgoing edge.
	for joinID := range parallelJoins {
		if count := len(forwardEdges[joinID]); count > 1 {
			return fmt.Errorf("PARALLEL_JOIN node %q cannot have more than 1 outgoing edge, got %d", joinID, count)
		}
	}

	// 4. Sub-graph topological containment, same shape as the batch gateway check.
	for splitID, joinID := range pairedJoins {
		if err := validateGatewayRegion(splitID, joinID, GatewayTypeParallelSplit, "PARALLEL_SPLIT", "PARALLEL_JOIN", "parallel region", pairedJoins, nodesByID, forwardEdges, reverseEdges); err != nil {
			return err
		}
	}

	return nil
}
