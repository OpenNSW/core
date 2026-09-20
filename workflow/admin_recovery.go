// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/OpenNSW/core/shared/maputil"
)

// adminParkNotificationTimeout bounds how long parkNodeForAdmin waits on the AdminParkActivity
// before giving up on this attempt's notification and parking anyway. Notification is
// best-effort: a slow or broken handler must never hold up the node actually parking.
const adminParkNotificationTimeout = 30 * time.Second

// terminalAdminError wraps an error that has already been through parkNodeForAdmin and was
// deliberately given up on (AdminActionAbort, or the resolution channel itself failed). It
// must propagate all the way up to GraphInterpreterWorkflow and fail the workflow without
// being caught and re-parked again by every ancestor node along the transitionTo call chain.
type terminalAdminError struct {
	err error
}

func (e *terminalAdminError) Error() string { return e.err.Error() }
func (e *terminalAdminError) Unwrap() error { return e.err }

// terminalAdminErrorType is the Go type name Temporal's default error converter stamps onto
// the ApplicationError it wraps a plain (non-Temporal) error in when that error crosses a
// child workflow boundary — e.g. a PARALLEL_SPLIT or BATCH_SPLIT branch's terminalAdminError,
// returned from the child, re-materializes in the parent as *temporal.ApplicationError with
// this Type(), not as a *terminalAdminError errors.As can find. Must match the unqualified
// type name of terminalAdminError exactly.
const terminalAdminErrorType = "terminalAdminError"

// isTerminalAdminError reports whether err has already been through parkNodeForAdmin and
// was deliberately given up on, meaning it should propagate without being parked again. This
// checks both forms: a same-workflow error still holding its original Go type, and one that
// crossed a child workflow boundary (a spawned PARALLEL_SPLIT or BATCH_SPLIT branch) and so
// only carries the type name as a string — see terminalAdminErrorType.
func isTerminalAdminError(err error) bool {
	var terminal *terminalAdminError
	if errors.As(err, &terminal) {
		return true
	}
	var appErr *temporal.ApplicationError
	return errors.As(err, &appErr) && appErr.Type() == terminalAdminErrorType
}

// AdminResolutionAction describes how an admin chooses to resolve a node that is
// parked in NodeStatusAwaitingAdmin.
type AdminResolutionAction string

// Admin resolution actions.
const (
	// AdminActionRetry applies WorkflowVariablesPatch, then re-runs the node's handler from
	// scratch. It is the admin's responsibility to ensure this is safe — e.g. a TASK node with a
	// populated CachedTaskResult has already run its Activity, so retrying re-invokes it.
	AdminActionRetry AdminResolutionAction = "RETRY"
	// AdminActionComplete applies WorkflowVariablesPatch and marks the node completed without
	// running it (or re-running it): the patch stands in for the node's output. An empty patch
	// just moves past the node. Always safe, but not allowed for GATEWAY nodes.
	AdminActionComplete AdminResolutionAction = "COMPLETE"
	// AdminActionAbort fails the node and the workflow with the original error.
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
	// WorkflowVariablesPatch sets workflow variables before the action takes effect, for
	// AdminActionRetry and AdminActionComplete. Keys are dotted paths (e.g. "review.outcome"),
	// applied in sorted order, and each value is written the way a task's output mapping writes
	// one: a map value is merged into an existing map at that path (fields it doesn't name are
	// kept), and any other value replaces what is there. It is a patch, not the full variable set
	// — variables not named here are untouched. It is not an RFC 6902 JSON Patch, and a nil value
	// sets the path to nil rather than deleting it. The variables are workflow-wide and persist,
	// so they affect every later node too.
	WorkflowVariablesPatch map[string]any `json:"workflow_variables_patch,omitempty"`
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
			selector := workflow.NewSelector(ctx)
			var sig AdminResolutionSignal
			var ok bool

			selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
				c.Receive(ctx, &sig)
				ok = true
			})
			selector.AddReceive(ctx.Done(), func(c workflow.ReceiveChannel, more bool) {
			})

			selector.Select(ctx)
			if ctx.Err() != nil || !ok {
				return
			}
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
// explicit warning so an admin doesn't blindly Retry and re-invoke it — Complete is the
// safe choice in that case.
func parkedErrorMessage(cause error, cachedTaskResult map[string]any) string {
	if cachedTaskResult != nil {
		return fmt.Sprintf("%s (WARNING: the Activity already completed successfully — use COMPLETE instead of RETRY to avoid re-running it)", cause.Error())
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

		notified := g.notifyAdminPark(ctx, node, nodeInfo)

		sig, err := g.awaitAdminResolution(ctx, node.ID)
		// Join notifyAdminPark's background coroutine before acting on the resolution (on every
		// path below, including returns): a prompt signal could otherwise let parkNodeForAdmin
		// return, and Temporal abandons workflow.Go coroutines that haven't finished when the
		// workflow function returns — dropping a handler error notifyAdminPark hasn't yet
		// recorded on AuditTrail. Joining here only after the resolution arrives, rather than
		// unconditionally up front, keeps this from blocking node parking on the notification
		// (see notifyAdminPark's own doc for why that ordering matters).
		_ = notified.Get(ctx, nil)
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

		case AdminActionComplete:
			// GATEWAY nodes route to one of several outEdges based on conditions (or fan
			// out to all of them for a parallel split) — blindly completing into
			// outEdges[0] would ignore that routing entirely, silently taking the wrong
			// branch or breaking a downstream parallel join. Steer the admin to Retry
			// instead, which re-runs the gateway's real (side-effect-free) routing logic.
			if node.Type == NodeTypeGateway {
				workflow.GetLogger(ctx).Warn("complete is not supported for GATEWAY nodes; use Retry after correcting variables, or Abort", "node_id", node.ID)
				continue
			}
			return g.completeParkedNode(ctx, nodeInfo, outEdges, sig.WorkflowVariablesPatch)

		case AdminActionRetry:
			applyVariablesPatch(g.instance.WorkflowVariables, sig.WorkflowVariablesPatch)
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
			workflow.GetLogger(ctx).Warn("ignoring unrecognized admin resolution action", "node_id", node.ID, "action", sig.Action)
			continue
		}
	}
}

// notifyAdminPark fires AdminParkActivity in a background coroutine so the host application's
// registered AdminParkHandler (if any) can surface this park event however it chooses, without
// making the caller yield first. That matters: the caller must reach awaitAdminResolution's
// pendingAdminResolutions registration in the same tick, with no yield in between — a signal
// (whether a real admin action or, as a batch/parallel gateway test's zero-delay callback
// demonstrated, a near-instant one) arriving before that registration exists is silently dropped
// by startAdminResolutionDispatcher. Blocking here on the notification first, even briefly,
// reopens exactly that window.
//
// Best-effort beyond that: bounded by adminParkNotificationTimeout, and any failure (timeout, no
// handler registered — which AdminParkActivity itself treats as success, or the handler
// returning an error) is recorded on the workflow's own AuditTrail rather than propagated.
//
// The returned Future settles once the coroutine has fully finished, including that AuditTrail
// append — not merely once the Activity call itself returns. Temporal abandons a workflow.Go
// coroutine outright if the workflow function returns before the coroutine finishes, so a caller
// that only cared about the Activity call completing could still race the append: callers must
// join this Future (see parkNodeForAdmin) before letting the workflow reach a point where it
// might return.
func (g *graphInterpreter) notifyAdminPark(ctx workflow.Context, node *Node, nodeInfo *NodeInfo) workflow.Future {
	payload := AdminParkPayload{
		WorkflowID:       workflow.GetInfo(ctx).WorkflowExecution.ID,
		RunID:            workflow.GetInfo(ctx).WorkflowExecution.RunID,
		RootWorkflowID:   g.rootWorkflowID(),
		NodeID:           node.ID,
		NodeType:         string(node.Type),
		TaskTemplateID:   node.TaskTemplateID,
		Cause:            nodeInfo.LastError,
		CachedTaskResult: nodeInfo.CachedTaskResult,
	}
	future, settable := workflow.NewFuture(ctx)
	workflow.Go(ctx, func(gCtx workflow.Context) {
		actCtx := workflow.WithActivityOptions(gCtx, workflow.ActivityOptions{
			StartToCloseTimeout: adminParkNotificationTimeout,
			// A best-effort, fire-and-record notification isn't worth retrying: a permanently
			// failing handler would otherwise retry indefinitely under Temporal's default retry
			// policy (unbounded without a ScheduleToCloseTimeout), and since parkNodeForAdmin now
			// joins this call's completion before proceeding, an indefinite retry loop here would
			// hang the whole node — not just this notification — waiting on it.
			RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		if err := workflow.ExecuteActivity(actCtx, "AdminParkActivity", payload).Get(gCtx, nil); err != nil {
			g.instance.AuditTrail = append(g.instance.AuditTrail,
				fmt.Sprintf("node %s: admin park notification failed: %s", node.ID, err.Error()))
		}
		settable.Set(nil, nil)
	})
	return future
}

// applyVariablesPatch writes each dotted path in patch into vars, in sorted key order, so
// overlapping keys (a and a.b) resolve the same way on every run. Values go through
// maputil.SetNestedKey, so a map merges into an existing map and anything else replaces it.
func applyVariablesPatch(vars, patch map[string]any) {
	for _, k := range slices.Sorted(maps.Keys(patch)) {
		maputil.SetNestedKey(vars, k, patch[k])
	}
}

// completeParkedNode marks a parked node Completed and transitions onward via its first
// outgoing edge, applying patch to WorkflowVariables first (a nil or empty patch changes nothing).
func (g *graphInterpreter) completeParkedNode(ctx workflow.Context, nodeInfo *NodeInfo, outEdges []Edge, patch map[string]any) error {
	applyVariablesPatch(g.instance.WorkflowVariables, patch)
	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.LastError = ""
	nodeInfo.CachedTaskResult = nil
	nodeInfo.UpdatedAt = workflow.Now(ctx)
	if len(outEdges) > 0 {
		return g.transitionTo(ctx, outEdges[0])
	}
	return nil
}
