// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"fmt"

	"go.temporal.io/sdk/workflow"

	"github.com/OpenNSW/core/shared/maputil"
)

// handleSignalingNode executes a SIGNALING node (EMIT or WAIT sub-type).
//
// EMIT: fires a signal asynchronously to sibling branches spawned by the same
// SPLIT_TASK node. The signal travels one hop up to the parent, which then
// rebroadcasts it to that node's other children. It does not bubble further up
// an ancestor chain or cascade down into a sibling's own nested sub-splits.
// Safe to use standalone (gracefully warns if no parent workflow ID is set).
//
// WAIT: suspends the workflow coroutine until a signal with the matching name
// arrives (routed down from the parent broker). The received payload is written
// back into WorkflowVariables via the node's OutputMapping.
func (g *graphInterpreter) handleSignalingNode(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge) error {
	cfg := node.Signaling
	if cfg == nil {
		return fmt.Errorf("SIGNALING node %s: signaling config is required", node.ID)
	}
	if cfg.SignalName == "" {
		return fmt.Errorf("SIGNALING node %s: signal_name is required", node.ID)
	}

	switch cfg.Type {
	case SignalingTypeEmit:
		if err := g.handleSignalingEmit(ctx, nodeInfo, node, cfg); err != nil {
			return err
		}

	case SignalingTypeWait:
		if err := g.handleSignalingWait(ctx, nodeInfo, node, cfg); err != nil {
			return err
		}

	default:
		return fmt.Errorf("SIGNALING node %s: unknown signaling type %q", node.ID, cfg.Type)
	}

	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)

	if len(outEdges) > 0 {
		return g.transitionTo(ctx, outEdges[0])
	}
	return nil
}

// handleSignalingEmit fires a signal from this child workflow up to its parent,
// which then rebroadcasts it to all sibling branches of the same SPLIT_TASK node
// (excluding the sender). If no parent is registered the operation is a no-op
// with a warning — this lets EMIT nodes appear in top-level workflows without
// causing failures.
//
// The configured Payload is used as-is; additional payload fields can be injected
// by populating node.InputMapping.
func (g *graphInterpreter) handleSignalingEmit(ctx workflow.Context, _ *NodeInfo, node *Node, cfg *SignalingConfig) error {
	signalName := cfg.SignalName

	parentWorkflowID, _ := g.instance.WorkflowVariables[VarParentWorkflowID].(string)
	if parentWorkflowID == "" {
		workflow.GetLogger(ctx).Warn("SIGNALING EMIT: _parent_workflow_id not set; signal cannot be sent to parent",
			"node", node.ID)
		return nil
	}

	branchID, _ := g.instance.WorkflowVariables[VarBranchID].(string)
	if branchID == "" {
		workflow.GetLogger(ctx).Warn("SIGNALING EMIT: _branch_id not set; signal will be unfiltered",
			"node", node.ID)
	}

	splitNodeID, _ := g.instance.WorkflowVariables[VarSplitNodeID].(string)

	// Build payload: start from the static config payload, then apply InputMapping overrides.
	payload := make(map[string]any, len(cfg.Payload))
	for k, v := range cfg.Payload {
		payload[k] = v
	}
	if len(node.InputMapping) > 0 {
		for rawGlobalKey, localKey := range node.InputMapping {
			globalKey, optional := parseMappingKey(rawGlobalKey)
			val, exists := maputil.GetNestedKey(g.instance.WorkflowVariables, globalKey)
			if !exists {
				if optional {
					continue
				}
				return fmt.Errorf("SIGNALING EMIT node %s: input mapping error: required global variable %q not found", node.ID, globalKey)
			}
			maputil.SetNestedKey(payload, localKey, val)
		}
	}

	msg := BroadcastMessage{
		SenderBranchID: branchID,
		SignalName:     signalName,
		Payload:        payload,
	}

	// Non-blocking fire-and-forget: signal delivery up to the parent, which rebroadcasts
	// to this node's siblings only — one hop up, one hop back down. See README.md.
	future := workflow.SignalExternalWorkflow(ctx, parentWorkflowID, "", childBroadcastSignalName(splitNodeID), msg)
	workflow.Go(ctx, func(ctx workflow.Context) {
		if err := future.Get(ctx, nil); err != nil {
			workflow.GetLogger(ctx).Error("SIGNALING EMIT: failed to send signal to parent",
				"node", node.ID, "parent", parentWorkflowID, "error", err)
			g.instance.AuditTrail = append(g.instance.AuditTrail,
				fmt.Sprintf("SIGNALING EMIT: failed to send signal to parent %s: %s", parentWorkflowID, err.Error()))
		}
	})
	return nil
}

// handleSignalingWait blocks the workflow coroutine until a signal with the
// matching name is received. The received payload is written back into
// WorkflowVariables using the node's OutputMapping. The received data is cached
// on NodeInfo so that if output mapping fails and the node parks for admin, a
// subsequent retry does not re-block waiting for the signal again.
func (g *graphInterpreter) handleSignalingWait(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, cfg *SignalingConfig) error {
	signalName := cfg.SignalName

	var signalData map[string]any

	if nodeInfo.CachedTaskResult != nil {
		// Signal already received on a previous attempt that parked in output mapping.
		signalData = nodeInfo.CachedTaskResult
	} else {
		signalChan := workflow.GetSignalChannel(ctx, signalName)
		selector := workflow.NewSelector(ctx)
		var received bool

		selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, &signalData)
			received = true
		})
		selector.AddReceive(ctx.Done(), func(_ workflow.ReceiveChannel, _ bool) {
			// Unblocks when context is canceled.
		})

		selector.Select(ctx)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		if received {
			// Cache so an admin reviewing a parked node (if output mapping fails) can
			// see the signal already arrived and we won't block again on retry.
			nodeInfo.CachedTaskResult = signalData
		}
	}

	if err := g.mapTaskOutputs(g.instance.WorkflowVariables, node.OutputMapping, signalData); err != nil {
		return err
	}
	nodeInfo.CachedTaskResult = nil
	return nil
}
