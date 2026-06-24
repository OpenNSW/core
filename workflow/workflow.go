// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/workflow"

	"github.com/OpenNSW/core/internal/maputil"
)

// graphInterpreter holds the state for a single workflow execution.
type graphInterpreter struct {
	def        WorkflowDefinition
	instance   *WorkflowInstance
	edgeTokens map[string]int

	// Pre-computed indexes for O(1) lookups
	nodes    map[string]*Node
	outEdges map[string][]Edge
	inEdges  map[string][]Edge

	// pendingAdminResolutions holds a Settable for each node currently parked in
	// NodeStatusAwaitingAdmin, keyed by node template ID. See admin_recovery.go.
	pendingAdminResolutions map[string]workflow.Settable
}

// GraphInterpreterWorkflow is the entry point for the Temporal workflow that interprets a graph definition.
func GraphInterpreterWorkflow(ctx workflow.Context, def WorkflowDefinition, initialWorkflowVariables map[string]any) (*WorkflowInstance, error) {
	if initialWorkflowVariables == nil {
		initialWorkflowVariables = make(map[string]any)
	}

	instance := &WorkflowInstance{
		ID:                workflow.GetInfo(ctx).WorkflowExecution.ID,
		Status:            StatusRunning,
		WorkflowVariables: initialWorkflowVariables,
		AuditTrail:        make([]string, 0),
		NodeInfo:          make(map[string]*NodeInfo),
		Edges:             make([]Edge, len(def.Edges)),
	}

	// Generate UUIDs deterministically
	var generatedUUIDs map[string]string
	if err := workflow.SideEffect(ctx, func(_ workflow.Context) interface{} {
		uuids := make(map[string]string)
		for _, node := range def.Nodes {
			uuids[node.ID] = uuid.NewString()
		}
		return uuids
	}).Get(&generatedUUIDs); err != nil {
		return nil, fmt.Errorf("failed to generate UUIDs via SideEffect: %w", err)
	}

	for _, node := range def.Nodes {
		instance.NodeInfo[node.ID] = &NodeInfo{
			// Create a unique ID for the node. node.ID is the ID in our template.
			ID:             node.ID + ":" + generatedUUIDs[node.ID],
			Type:           node.Type,
			GatewayType:    node.GatewayType,
			TaskTemplateID: node.TaskTemplateID,
			CreatedAt:      workflow.Now(ctx),
			UpdatedAt:      workflow.Now(ctx),
			Status:         NodeStatusNotStarted,
		}
	}

	// Resolve Source and Target IDs in edges to the generated node instance IDs
	for i, edge := range def.Edges {
		sourceNodeInfo, sourceExists := instance.NodeInfo[edge.SourceID]
		if !sourceExists {
			return nil, fmt.Errorf("invalid edge definition: source node '%s' not found for edge '%s'", edge.SourceID, edge.ID)
		}
		targetNodeInfo, targetExists := instance.NodeInfo[edge.TargetID]
		if !targetExists {
			return nil, fmt.Errorf("invalid edge definition: target node '%s' not found for edge '%s'", edge.TargetID, edge.ID)
		}
		instance.Edges[i] = Edge{
			ID:        edge.ID,
			SourceID:  sourceNodeInfo.ID,
			TargetID:  targetNodeInfo.ID,
			Condition: edge.Condition,
		}
	}

	// Initialize our interpreter struct
	interp := &graphInterpreter{
		def:                     def,
		instance:                instance,
		edgeTokens:              make(map[string]int),
		pendingAdminResolutions: make(map[string]workflow.Settable),
	}
	interp.buildIndexes()

	if err := workflow.SetQueryHandler(ctx, "GetStatus", func() (*WorkflowInstance, error) {
		return instance, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to set GetStatus query handler: %w", err)
	}

	signalChan := workflow.GetSignalChannel(ctx, "TaskUpdateSignal")
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var updateEvent UpdateEvent
			signalChan.Receive(ctx, &updateEvent)
			// TODO: implement event handling
		}
	})

	interp.startAdminResolutionDispatcher(ctx)

	ao := workflow.ActivityOptions{StartToCloseTimeout: 24 * time.Hour * 365}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Begin Execution
	startNode := interp.findStartNode()
	if startNode == nil {
		instance.Status = StatusFailed
		return instance, fmt.Errorf("no START node found")
	}

	if err := interp.executeNode(ctx, startNode.ID); err != nil {
		instance.Status = StatusFailed
		return instance, err
	}

	instance.Status = StatusCompleted
	return instance, nil
}

// buildIndexes pre-computes node and edge lookups for performance and cleanliness
func (g *graphInterpreter) buildIndexes() {
	g.nodes = make(map[string]*Node)
	g.outEdges = make(map[string][]Edge)
	g.inEdges = make(map[string][]Edge)

	for i, n := range g.def.Nodes {
		g.nodes[n.ID] = &g.def.Nodes[i]
	}
	for _, e := range g.def.Edges {
		g.outEdges[e.SourceID] = append(g.outEdges[e.SourceID], e)
		g.inEdges[e.TargetID] = append(g.inEdges[e.TargetID], e)
	}
}

func (g *graphInterpreter) findStartNode() *Node {
	for _, n := range g.def.Nodes {
		if n.Type == NodeTypeStart {
			return &n
		}
	}
	return nil
}

func (g *graphInterpreter) transitionTo(ctx workflow.Context, edge Edge) error {
	g.edgeTokens[edge.ID]++
	return g.executeNode(ctx, edge.TargetID)
}

// dispatchNodeHandler delegates to the handler for node's type. It is used both for normal
// execution and to re-run a parked node's handler from scratch on AdminActionRetry.
func (g *graphInterpreter) dispatchNodeHandler(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge) error {
	switch node.Type {
	case NodeTypeStart:
		return g.handleStartNode(ctx, nodeInfo, outEdges)
	case NodeTypeTask:
		return g.handleTaskNode(ctx, nodeInfo, node, outEdges)
	case NodeTypeGateway:
		return g.handleGatewayNode(ctx, nodeInfo, node, outEdges)
	case NodeTypeSplitTask:
		return g.handleSplitTaskNode(ctx, nodeInfo, node, outEdges)
	case NodeTypeEnd:
		return g.handleEndNode(ctx, nodeInfo)
	default:
		return fmt.Errorf("unknown node type: %v", node.Type)
	}
}

func (g *graphInterpreter) executeNode(ctx workflow.Context, nodeID string) error {
	nodeInfo := g.instance.NodeInfo[nodeID]
	node, exists := g.nodes[nodeID]

	if !exists || nodeInfo == nil {
		// Unreachable in practice: every edge's source/target is validated against
		// NodeInfo before execution begins (see GraphInterpreterWorkflow). There is no
		// NodeInfo to park on here, so this stays a hard failure rather than going
		// through the admin escape hatch.
		return fmt.Errorf("node %s not found", nodeID)
	}

	// Set node to Running for all node types at entry
	nodeInfo.Status = NodeStatusRunning
	nodeInfo.UpdatedAt = workflow.Now(ctx)

	outEdges := g.outEdges[node.ID]

	err := g.dispatchNodeHandler(ctx, nodeInfo, node, outEdges)
	if err == nil {
		return nil
	}
	if isTerminalAdminError(err) {
		// This error already went through parkNodeForAdmin for a downstream node (it
		// transitioned here, or further, before being deliberately aborted) — propagate
		// as-is rather than treating it as a fresh failure of this node.
		return err
	}
	return g.parkNodeForAdmin(ctx, nodeInfo, node, outEdges, err)
}

// handleStartNode transitions to the single outgoing edge and marks itself Completed.
func (g *graphInterpreter) handleStartNode(ctx workflow.Context, nodeInfo *NodeInfo, outEdges []Edge) error {
	if len(outEdges) == 0 {
		return fmt.Errorf("START node has no outgoing edges")
	}
	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)
	return g.transitionTo(ctx, outEdges[0])
}

// handleEndNode fires WorkflowCompletedActivity and marks itself Completed.
func (g *graphInterpreter) handleEndNode(ctx workflow.Context, nodeInfo *NodeInfo) error {
	err := workflow.ExecuteActivity(ctx, "WorkflowCompletedActivity", g.instance.ID, g.instance.WorkflowVariables).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("unable to complete workflow: %w", err)
	}
	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)
	return nil
}

func (g *graphInterpreter) mapTaskInputs(inputMapping map[string]string) (map[string]any, error) {
	inputs := make(map[string]any, len(inputMapping))
	if len(inputMapping) == 0 {
		return inputs, nil
	}

	for rawGlobalKey, localKey := range inputMapping {
		globalKey, optional := parseMappingKey(rawGlobalKey)
		val, exists := maputil.GetNestedKey(g.instance.WorkflowVariables, globalKey)
		if !exists {
			if optional {
				continue
			}
			return nil, fmt.Errorf("input mapping error: required global variable '%s' not found in workflow variables for task node", globalKey)
		}
		maputil.SetNestedKey(inputs, localKey, val)
	}

	return inputs, nil
}

func (g *graphInterpreter) mapTaskOutputs(workflowVars map[string]any, outputMapping map[string]string, result map[string]any) error {
	if len(outputMapping) == 0 || result == nil {
		return nil
	}

	for rawTaskKey, globalKey := range outputMapping {
		taskKey, optional := parseMappingKey(rawTaskKey)
		val, exists := maputil.GetNestedKey(result, taskKey)
		if !exists {
			if optional {
				continue
			}
			return fmt.Errorf("output mapping error: required task variable '%s' not found in task result", taskKey)
		}
		maputil.SetNestedKey(workflowVars, globalKey, val)
	}
	return nil
}

func (g *graphInterpreter) handleTaskNode(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge) error {
	inputs, err := g.mapTaskInputs(node.InputMapping)
	if err != nil {
		return err
	}

	var result map[string]any

	nodeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ActivityID:          nodeInfo.ID,
		StartToCloseTimeout: 24 * time.Hour * 365,
	})

	err = workflow.ExecuteActivity(nodeCtx, "ExecuteTaskActivity", node.TaskTemplateID, inputs).Get(ctx, &result)
	if err != nil {
		return err
	}
	// Cache the raw result so an admin reviewing a parked node (if mapTaskOutputs below
	// fails) can see the Activity already ran and what it returned, rather than blindly
	// retrying and re-invoking it.
	nodeInfo.CachedTaskResult = result

	err = g.mapTaskOutputs(g.instance.WorkflowVariables, node.OutputMapping, result)
	if err != nil {
		return err
	}
	nodeInfo.CachedTaskResult = nil

	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)

	if len(outEdges) > 0 {
		return g.transitionTo(ctx, outEdges[0])
	}
	return nil
}

func (g *graphInterpreter) handleGatewayNode(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge) error {
	inEdges := g.inEdges[node.ID]

	switch node.GatewayType {
	case GatewayTypeExclusiveSplit:
		for _, e := range outEdges {
			match, err := EvaluateCondition(e.Condition, g.instance.WorkflowVariables)
			if err != nil {
				return err
			}
			if match {
				nodeInfo.Status = NodeStatusCompleted
				nodeInfo.UpdatedAt = workflow.Now(ctx)
				return g.transitionTo(ctx, e)
			}
		}
		return fmt.Errorf("no matching conditions found at exclusive gateway %s", node.ID)

	case GatewayTypeParallelSplit:
		nodeInfo.Status = NodeStatusCompleted
		nodeInfo.UpdatedAt = workflow.Now(ctx)
		var futures []workflow.Future
		for _, e := range outEdges {
			match, err := EvaluateCondition(e.Condition, g.instance.WorkflowVariables)
			if err != nil {
				return err
			}
			if match {
				f, s := workflow.NewFuture(ctx)
				edge := e // Capture locally for coroutine
				workflow.Go(ctx, func(c workflow.Context) {
					err := g.transitionTo(c, edge)
					s.Set(nil, err)
				})
				futures = append(futures, f)
			}
		}
		for _, f := range futures {
			if err := f.Get(ctx, nil); err != nil {
				return err
			}
		}
		return nil

	case GatewayTypeParallelJoin:
		for _, e := range inEdges {
			if g.edgeTokens[e.ID] <= 0 {
				return nil // Wait for other branches
			}
		}
		for _, e := range inEdges {
			g.edgeTokens[e.ID]-- // Consume tokens
		}
		if len(outEdges) > 0 {
			nodeInfo.Status = NodeStatusCompleted
			nodeInfo.UpdatedAt = workflow.Now(ctx)
			return g.transitionTo(ctx, outEdges[0])
		}
		return nil

	case GatewayTypeExclusiveJoin:
		consumed := false
		for _, e := range inEdges {
			if g.edgeTokens[e.ID] > 0 {
				g.edgeTokens[e.ID]--
				consumed = true
				break
			}
		}
		if !consumed {
			return fmt.Errorf("exclusive join %s reached with no incoming token — invalid workflow definition", node.ID)
		}
		if len(outEdges) != 1 {
			return fmt.Errorf("exclusive join %s must have exactly one outgoing edge, got %d — invalid workflow definition", node.ID, len(outEdges))
		}
		nodeInfo.Status = NodeStatusCompleted
		nodeInfo.UpdatedAt = workflow.Now(ctx)
		return g.transitionTo(ctx, outEdges[0])
	default:
		return fmt.Errorf("unknown gateway type: %v", node.GatewayType)
	}
}
