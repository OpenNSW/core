// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"errors"
	"fmt"

	"go.temporal.io/sdk/workflow"

	"github.com/OpenNSW/core/internal/maputil"
)

// terminalAdminError wraps an error that has already been through parkNodeForAdmin and was
// deliberately given up on (AdminActionAbort, or the resolution channel itself failed). It
// must propagate all the way up to GraphInterpreterWorkflow and fail the workflow without
// being caught and re-parked again by every ancestor node along the transitionTo call chain.
type terminalAdminError struct {
	err error
}

func (e *terminalAdminError) Error() string { return e.err.Error() }
func (e *terminalAdminError) Unwrap() error { return e.err }

// isTerminalAdminError reports whether err has already been through parkNodeForAdmin and
// was deliberately given up on, meaning it should propagate without being parked again.
func isTerminalAdminError(err error) bool {
	var terminal *terminalAdminError
	return errors.As(err, &terminal)
}

// AdminResolutionAction describes how an admin chooses to resolve a node that is
// parked in NodeStatusAwaitingAdmin.
type AdminResolutionAction string

// Admin resolution actions.
const (
	// AdminActionRetry re-runs the node's handler from scratch. It is the admin's
	// responsibility to ensure this is safe — e.g. a TASK node with a populated
	// CachedTaskResult has already run its Activity, so retrying re-invokes it.
	AdminActionRetry AdminResolutionAction = "RETRY"
	// AdminActionOverride merges Overrides directly into WorkflowVariables and marks
	// the node completed without re-running anything. Always safe.
	AdminActionOverride AdminResolutionAction = "OVERRIDE"
	// AdminActionSkip marks the node completed without setting any variables.
	AdminActionSkip AdminResolutionAction = "SKIP"
	// AdminActionAbort fails the node and the workflow with the original error —
	// the same behavior the engine had before the escape hatch existed.
	AdminActionAbort AdminResolutionAction = "ABORT"
)

// AdminResolutionSignalName is the Temporal signal name used to deliver AdminResolutionSignal.
const AdminResolutionSignalName = "AdminResolutionSignal"

// AdminResolutionSignal is sent by an external admin tool to resolve a node that is
// parked in NodeStatusAwaitingAdmin.
type AdminResolutionSignal struct {
	// NodeID is the template node ID (Node.ID) of the parked node — the routing key.
	NodeID string `json:"nodeID"`
	// Action determines how the node is resolved. See AdminResolutionAction constants.
	Action AdminResolutionAction `json:"action"`
	// Overrides are merged into WorkflowVariables for AdminActionOverride/AdminActionSkip,
	// or merged in before retrying for AdminActionRetry.
	Overrides map[string]any `json:"overrides,omitempty"`
	// Reason is a free-text admin justification, appended to the workflow's AuditTrail.
	Reason string `json:"reason,omitempty"`
}

// startAdminResolutionDispatcher registers the AdminResolutionSignal channel and routes
// incoming signals to whichever node is currently parked awaiting that NodeID, via
// g.pendingAdminResolutions. Signals for an unknown or already-resolved NodeID are dropped —
// this includes a signal sent for a node that hasn't parked yet (e.g. sent too early, before
// the admin confirmed AWAITING_ADMIN via GetStatus). This is intentional: the engine does not
// buffer premature signals, to keep the resolution path simple. Callers/tools are expected to
// confirm a node is actually parked before resolving it.
func (g *graphInterpreter) startAdminResolutionDispatcher(ctx workflow.Context) {
	signalChan := workflow.GetSignalChannel(ctx, AdminResolutionSignalName)
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var sig AdminResolutionSignal
			signalChan.Receive(ctx, &sig)
			if settable, ok := g.pendingAdminResolutions[sig.NodeID]; ok {
				settable.Set(sig, nil)
				delete(g.pendingAdminResolutions, sig.NodeID)
			}
		}
	})
}

// awaitAdminResolution blocks the calling coroutine until an AdminResolutionSignal
// arrives for nodeID.
func (g *graphInterpreter) awaitAdminResolution(ctx workflow.Context, nodeID string) (AdminResolutionSignal, error) {
	future, settable := workflow.NewFuture(ctx)
	g.pendingAdminResolutions[nodeID] = settable
	defer delete(g.pendingAdminResolutions, nodeID)

	var sig AdminResolutionSignal
	err := future.Get(ctx, &sig)
	return sig, err
}

// parkedErrorMessage builds the LastError text shown for a parked node. If the node's
// Activity already completed successfully (cachedTaskResult is populated), it appends an
// explicit warning so an admin doesn't blindly Retry and re-invoke it — Override is the
// safe choice in that case.
func parkedErrorMessage(cause error, cachedTaskResult map[string]any) string {
	if cachedTaskResult != nil {
		return fmt.Sprintf("%s (WARNING: the Activity already completed successfully — use OVERRIDE instead of RETRY to avoid re-running it)", cause.Error())
	}
	return cause.Error()
}

// parkNodeForAdmin is invoked by executeNode whenever a node handler returns an error.
// Instead of failing the workflow, it marks the node NodeStatusAwaitingAdmin and blocks
// (only this node's execution path — sibling parallel branches are unaffected) until an
// admin resolves it via AdminResolutionSignal.
func (g *graphInterpreter) parkNodeForAdmin(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge, cause error) error {
	for {
		nodeInfo.Status = NodeStatusAwaitingAdmin
		nodeInfo.LastError = parkedErrorMessage(cause, nodeInfo.CachedTaskResult)
		nodeInfo.UpdatedAt = workflow.Now(ctx)
		g.instance.AuditTrail = append(g.instance.AuditTrail,
			fmt.Sprintf("node %s parked for admin intervention: %s", node.ID, nodeInfo.LastError))

		sig, err := g.awaitAdminResolution(ctx, node.ID)
		if err != nil {
			nodeInfo.Status = NodeStatusFailed
			return &terminalAdminError{err: err}
		}
		g.instance.AuditTrail = append(g.instance.AuditTrail,
			fmt.Sprintf("node %s admin resolution: %s (%s)", node.ID, sig.Action, sig.Reason))

		switch sig.Action {
		case AdminActionAbort:
			nodeInfo.Status = NodeStatusFailed
			return &terminalAdminError{err: cause}

		case AdminActionSkip:
			// GATEWAY nodes route to one of several outEdges based on conditions (or fan
			// out to all of them for a parallel split) — blindly completing into
			// outEdges[0] would ignore that routing entirely, silently taking the wrong
			// branch or breaking a downstream parallel join. Steer the admin to Retry
			// instead, which re-runs the gateway's real (side-effect-free) routing logic.
			if node.Type == NodeTypeGateway {
				workflow.GetLogger(ctx).Warn("Skip is not supported for GATEWAY nodes; use Retry after correcting variables, or Abort", "nodeID", node.ID)
				continue
			}
			return g.completeParkedNode(ctx, nodeInfo, outEdges, nil)

		case AdminActionOverride:
			if node.Type == NodeTypeGateway {
				workflow.GetLogger(ctx).Warn("Override is not supported for GATEWAY nodes; use Retry after correcting variables, or Abort", "nodeID", node.ID)
				continue
			}
			return g.completeParkedNode(ctx, nodeInfo, outEdges, sig.Overrides)

		case AdminActionRetry:
			for k, v := range sig.Overrides {
				maputil.SetNestedKey(g.instance.WorkflowVariables, k, v)
			}
			nodeInfo.Status = NodeStatusRunning
			nodeInfo.LastError = ""
			// Clear any cached Activity result from the previous attempt before re-dispatching:
			// if this retry fails again before reaching the Activity (e.g. input mapping fails),
			// the stale result must not linger and falsely suggest the Activity ran this time.
			nodeInfo.CachedTaskResult = nil
			nodeInfo.UpdatedAt = workflow.Now(ctx)
			retryErr := g.dispatchNodeHandler(ctx, nodeInfo, node, outEdges)
			if retryErr == nil {
				return nil
			}
			if isTerminalAdminError(retryErr) {
				// The retry succeeded and transitioned onward; this error came from a
				// downstream node that already went through (and gave up on) its own
				// admin resolution. Propagate as-is rather than parking this node again.
				return retryErr
			}
			cause = retryErr
			continue

		default:
			workflow.GetLogger(ctx).Warn("ignoring unrecognized admin resolution action", "nodeID", node.ID, "action", sig.Action)
			continue
		}
	}
}

// completeParkedNode marks a parked node Completed and transitions onward via its first
// outgoing edge, optionally merging overrides into WorkflowVariables first. overrides is nil
// for AdminActionSkip (no variables touched) and sig.Overrides for AdminActionOverride.
func (g *graphInterpreter) completeParkedNode(ctx workflow.Context, nodeInfo *NodeInfo, outEdges []Edge, overrides map[string]any) error {
	for k, v := range overrides {
		maputil.SetNestedKey(g.instance.WorkflowVariables, k, v)
	}
	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.LastError = ""
	nodeInfo.CachedTaskResult = nil
	nodeInfo.UpdatedAt = workflow.Now(ctx)
	if len(outEdges) > 0 {
		return g.transitionTo(ctx, outEdges[0])
	}
	return nil
}
