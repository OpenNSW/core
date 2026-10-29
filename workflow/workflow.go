// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/OpenNSW/core/shared/maputil"
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

	// seq is the workflow-wide step counter: it is advanced each time any node starts a run, and
	// its value identifies that run (see mintActivation). It only ever grows.
	seq int64

	// pendingAdminResolutions holds a Settable for each node currently parked in
	// NodeStatusAwaitingAdmin, keyed by that park's ActivationID. See admin_recovery.go.
	pendingAdminResolutions map[string]workflow.Settable

	// pendingCompletions holds a Settable for each TASK step that may be completed through
	// CompleteActivation, keyed by its ActivationID. See completion.go.
	pendingCompletions map[string]workflow.Settable
}

// GraphInterpreterWorkflow is the entry point for the Temporal workflow that interprets a graph definition.
func GraphInterpreterWorkflow(ctx workflow.Context, def WorkflowDefinition, initialWorkflowVariables map[string]any) (*WorkflowInstance, error) {
	if initialWorkflowVariables == nil {
		initialWorkflowVariables = make(map[string]any)
	}

	// TODO: Implement comprehensive workflow definition validation (e.g. reachable nodes, dangling edges,
	// start/end node counts, gateway pairings). Currently only validating batch gateway invariants.
	if err := ValidateBatchGateways(def); err != nil {
		return nil, fmt.Errorf("workflow definition validation failed: %w", err)
	}
	if err := ValidateParallelGateways(def); err != nil {
		return nil, fmt.Errorf("workflow definition validation failed: %w", err)
	}

	instance := &WorkflowInstance{
		ID:                workflow.GetInfo(ctx).WorkflowExecution.ID,
		Status:            StatusRunning,
		WorkflowVariables: initialWorkflowVariables,
		AuditTrail:        make([]string, 0),
		NodeInfo:          make(map[string]*NodeInfo),
		Edges:             make([]Edge, len(def.Edges)),
	}

	// Only a top-level execution can reach this without a case — every nested child is started
	// with one already present in initialWorkflowVariables (copied through wholesale by
	// BATCH_SPLIT/PARALLEL_SPLIT, or set explicitly by SPLIT_TASK) — so a top-level workflow the
	// host started without a case, or with an empty one, is its own case. The check matches
	// caseID()'s, and the ID is stored rather than only returned, so every child copies a
	// non-empty case ID instead of falling back to its own workflow ID.
	if v, _ := instance.WorkflowVariables[VarCaseID].(string); v == "" {
		instance.WorkflowVariables[VarCaseID] = instance.ID
	}

	for _, node := range def.Nodes {
		instance.NodeInfo[node.ID] = &NodeInfo{
			ID:             node.ID,
			Type:           node.Type,
			GatewayType:    node.GatewayType,
			TaskTemplateID: node.TaskTemplateID,
			CreatedAt:      workflow.Now(ctx),
			UpdatedAt:      workflow.Now(ctx),
			Status:         NodeStatusNotStarted,
		}
	}

	for i, edge := range def.Edges {
		if _, ok := instance.NodeInfo[edge.SourceID]; !ok {
			return nil, fmt.Errorf("invalid edge definition: source node '%s' not found for edge '%s'", edge.SourceID, edge.ID)
		}
		if _, ok := instance.NodeInfo[edge.TargetID]; !ok {
			return nil, fmt.Errorf("invalid edge definition: target node '%s' not found for edge '%s'", edge.TargetID, edge.ID)
		}
		instance.Edges[i] = edge
	}

	// Initialize our interpreter struct
	interp := &graphInterpreter{
		def:                     def,
		instance:                instance,
		edgeTokens:              make(map[string]int),
		pendingAdminResolutions: make(map[string]workflow.Settable),
		pendingCompletions:      make(map[string]workflow.Settable),
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
			selector := workflow.NewSelector(ctx)
			var updateEvent UpdateEvent
			var ok bool

			selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
				c.Receive(ctx, &updateEvent)
				ok = true
			})
			selector.AddReceive(ctx.Done(), func(c workflow.ReceiveChannel, more bool) {
			})

			selector.Select(ctx)
			if ctx.Err() != nil || !ok {
				return
			}
			// TODO: implement event handling
		}
	})

	interp.startAdminResolutionDispatcher(ctx)
	if err := interp.registerCompletionHandler(ctx); err != nil {
		return nil, fmt.Errorf("failed to set %s update handler: %w", completeActivationUpdate, err)
	}

	// The END node's WorkflowCompletedActivity runs with these options. Every other Activity sets its
	// own (handleTaskNode's ExecuteTaskActivity among them).
	ao := workflow.ActivityOptions{StartToCloseTimeout: completionActivityTimeout, RetryPolicy: taskRetryPolicy}
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
	case NodeTypeTimer:
		return g.handleTimerNode(ctx, nodeInfo, node, outEdges)
	case NodeTypeSignaling:
		return g.handleSignalingNode(ctx, nodeInfo, node, outEdges)
	case NodeTypeEnd:
		return g.handleEndNode(ctx, nodeInfo)
	default:
		return withCategory(ParkCategoryDefinitionError, fmt.Errorf("unknown node type: %v", node.Type))
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

	g.mintActivation(nodeInfo, node)
	err := g.dispatchNodeHandler(ctx, nodeInfo, node, outEdges)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil || isTerminalAdminError(err) {
		// If the workflow was canceled, or this is a terminal admin error, propagate
		// the error directly without parking for admin.
		return err
	}
	return g.parkNodeForAdmin(ctx, nodeInfo, node, outEdges, err)
}

// handleStartNode transitions to the single outgoing edge and marks itself Completed.
func (g *graphInterpreter) handleStartNode(ctx workflow.Context, nodeInfo *NodeInfo, outEdges []Edge) error {
	if len(outEdges) == 0 {
		return withCategory(ParkCategoryDefinitionError, fmt.Errorf("START node has no outgoing edges"))
	}
	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)
	return g.transitionTo(ctx, outEdges[0])
}

// handleTimerNode waits for the configured duration, then follows its single
// outgoing edge. The wait is a durable Temporal timer: it holds no activity and
// no worker slot, and it resumes correctly after a worker restart.
//
// It increments a fire count in the workflow variables so a downstream gateway
// can bound a retry loop that would otherwise run forever (see
// TimerConfig.CounterKey).
//
// The count is written before the wait rather than after, which makes no
// difference to a downstream gateway — execution is sequential, so nothing
// after this node runs until the wait finishes either way. It matters to
// observers of the instance while the wait is in progress: a GetStatus query
// during the wait reports the attempt currently in flight, and an instance
// cancelled or terminated mid-wait is left with a counter and audit trail that
// account for the attempt it was on rather than lagging one behind.
func (g *graphInterpreter) handleTimerNode(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge) error {
	if node.Timer == nil || node.Timer.Duration == "" {
		return withCategory(ParkCategoryDefinitionError, fmt.Errorf("TIMER node %s: timer.duration is required", node.ID))
	}
	d, err := time.ParseDuration(node.Timer.Duration)
	if err != nil {
		return withCategory(ParkCategoryDefinitionError, fmt.Errorf("TIMER node %s: invalid timer.duration %q: %w", node.ID, node.Timer.Duration, err))
	}
	if d <= 0 {
		return withCategory(ParkCategoryDefinitionError, fmt.Errorf("TIMER node %s: timer.duration must be positive, got %s", node.ID, d))
	}
	// A TIMER is a pure delay on a single path; branching belongs to a gateway.
	if len(outEdges) != 1 {
		return withCategory(ParkCategoryDefinitionError, fmt.Errorf("TIMER node %s: expected exactly 1 outgoing edge, got %d", node.ID, len(outEdges)))
	}

	counterKey := node.Timer.CounterKey
	if counterKey == "" {
		counterKey = node.ID + ".iterations"
	}
	iterations := 1
	if prev, ok := maputil.GetNestedKey(g.instance.WorkflowVariables, counterKey); ok {
		iterations = asInt(prev) + 1
	}
	maputil.SetNestedKey(g.instance.WorkflowVariables, counterKey, iterations)

	g.instance.AuditTrail = append(g.instance.AuditTrail,
		fmt.Sprintf("TIMER %s waiting %s (iteration %d)", node.ID, d, iterations))

	// Cancellation (workflow termination) surfaces as an error here; propagate it
	// rather than falling through to the next node.
	if err := workflow.Sleep(ctx, d); err != nil {
		return err
	}

	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)
	return g.transitionTo(ctx, outEdges[0])
}

// asInt coerces a counter read back out of the workflow variables to an int.
// A value that has round-tripped through a Temporal payload arrives as float64
// rather than the int that was written.
func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float32:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

// handleEndNode fires WorkflowCompletedActivity (top-level workflows only) and marks itself Completed.
func (g *graphInterpreter) handleEndNode(ctx workflow.Context, nodeInfo *NodeInfo) error {
	// The completion hook fires only for the top-level workflow. A child workflow spawned by a
	// SPLIT_TASK node (any execution started with a Temporal parent) must not fire it: the hook is
	// documented as running when "the overall workflow" completes, it would be invoked with the
	// branch's synthetic child workflow ID (parentID--nodeID--branchID) that the host doesn't
	// recognize, and — critically — if the host handler errors on that unknown ID the END node
	// parks for admin and the branch never completes, hanging the parent's monitorChildWorkflows
	// forever. The parent aggregates each branch's result and fires the hook once when its own END
	// node is reached.
	if workflow.GetInfo(ctx).ParentWorkflowExecution == nil {
		// nodeInfo.Seq was minted by the caller (mintActivation) for this run of the END node, so the
		// host can order its completion write after every other node's writes.
		err := workflow.ExecuteActivity(ctx, "WorkflowCompletedActivity", g.instance.ID, g.instance.WorkflowVariables, nodeInfo.Seq).Get(ctx, nil)
		if err != nil {
			return withCategory(ParkCategoryTaskFailure, fmt.Errorf("unable to complete workflow: %w", err))
		}
	}
	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)
	return nil
}

// activationIDNamespace is the UUIDv5 namespace step IDs are derived in. It must never change: a step ID
// has to come out the same on every replay of the workflow.
var activationIDNamespace = uuid.MustParse("6f1c2c0e-5b58-4a55-9a4e-3d7a1f0c8b21")

// newActivationID returns the ID of one run of a node. It is a pure function of its inputs, so it is
// deterministic under Temporal replay without a side effect, and it is a valid UUID.
func newActivationID(workflowID, nodeID string, seq int64) string {
	return uuid.NewSHA1(activationIDNamespace, []byte(fmt.Sprintf("%s:%s:%d", workflowID, nodeID, seq))).String()
}

// mintActivation advances the workflow's step counter and records a fresh ActivationID on nodeInfo
// for this run of node. It is called once per dispatch of a node's handler — both a fresh
// execution and an admin retry — so a node revisited by a loop, or re-run by an admin action, is
// addressed as a new run: anything addressed to an earlier run no longer matches and is rejected.
func (g *graphInterpreter) mintActivation(nodeInfo *NodeInfo, node *Node) {
	g.seq++
	nodeInfo.ActivationID = newActivationID(g.instance.ID, node.ID, g.seq)
	nodeInfo.Seq = g.seq
}

// mapTaskInputs builds the task's inputs from the workflow variables.
func (g *graphInterpreter) mapTaskInputs(inputMapping map[string]string) (map[string]any, error) {
	inputs := make(map[string]any, len(inputMapping))
	if err := g.applyInputMapping(inputs, inputMapping); err != nil {
		return nil, err
	}
	return inputs, nil
}

// applyInputMapping writes each mapped workflow variable into dst under its mapped key, for every
// node type that reads input_mapping. Keys are visited in sorted order and every missing required
// variable is reported at once, so the error is the same on every run. If it returns an error dst
// may be partly written, so the caller must discard it.
func (g *graphInterpreter) applyInputMapping(dst map[string]any, inputMapping map[string]string) error {
	var missing []string
	for _, rawGlobalKey := range slices.Sorted(maps.Keys(inputMapping)) {
		globalKey, optional := parseMappingKey(rawGlobalKey)
		val, exists := maputil.GetNestedKey(g.instance.WorkflowVariables, globalKey)
		if !exists {
			if !optional {
				missing = append(missing, globalKey)
			}
			continue
		}
		maputil.SetNestedKey(dst, inputMapping[rawGlobalKey], val)
	}
	if len(missing) > 0 {
		return withCategory(ParkCategoryInputMapping, fmt.Errorf(
			"input mapping error: required global %s not found in workflow variables", describeVariables(missing)))
	}
	return nil
}

// mapTaskOutputs writes the task result into workflowVars per outputMapping. It is all-or-nothing:
// if any required field is missing from result, every missing one is reported (sorted) and nothing
// is written, so a failed mapping never leaves workflowVars half updated. A nil result is an empty
// one, so it fails on every required field rather than silently skipping the mapping.
func (g *graphInterpreter) mapTaskOutputs(workflowVars map[string]any, outputMapping map[string]string, result map[string]any) error {
	if len(outputMapping) == 0 {
		return nil
	}

	type write struct {
		globalKey string
		val       any
	}
	var writes []write
	var missing []string
	for _, rawTaskKey := range slices.Sorted(maps.Keys(outputMapping)) {
		taskKey, optional := parseMappingKey(rawTaskKey)
		val, exists := maputil.GetNestedKey(result, taskKey)
		if !exists {
			if !optional {
				missing = append(missing, taskKey)
			}
			continue
		}
		writes = append(writes, write{globalKey: outputMapping[rawTaskKey], val: val})
	}
	if len(missing) > 0 {
		return withCategory(ParkCategoryOutputMapping, fmt.Errorf(
			"output mapping error: required task %s not found in task result", describeVariables(missing)))
	}
	for _, w := range writes {
		maputil.SetNestedKey(workflowVars, w.globalKey, w.val)
	}
	return nil
}

// manifestVersion returns the artifact manifest version this execution resolves templates
// through (see VarManifestVersion), or 0 if the host set none. The variable passes through
// Temporal's JSON payloads, so a whole number arrives as a float64; a value that is not a
// positive whole number counts as none.
func (g *graphInterpreter) manifestVersion() int {
	switch v := g.instance.WorkflowVariables[VarManifestVersion].(type) {
	case int:
		return max(v, 0)
	case int64:
		return int(max(v, 0))
	case float64:
		if v > 0 && v == float64(int(v)) {
			return int(v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return int(n)
		}
	}
	return 0
}

// caseID returns the case this execution belongs to (see VarCaseID).
func (g *graphInterpreter) caseID() string {
	if v, ok := g.instance.WorkflowVariables[VarCaseID].(string); ok && v != "" {
		return v
	}
	return g.instance.ID
}

// taskActivityTimeout bounds one attempt of a task's Activity: running the step's plugin. A step that
// waits for a person ends its Activity first (see completion.go), so the wait doesn't count against
// it. An attempt whose worker died or whose call hangs is retried when this runs out.
const taskActivityTimeout = 10 * time.Minute

// completionActivityTimeout bounds one attempt of WorkflowCompletedActivity. The completion hook does
// its work and returns without waiting for anyone, so a few minutes is ample, and a worker that dies
// during an attempt is noticed when it runs out.
const completionActivityTimeout = 5 * time.Minute

// taskRetryPolicy bounds the retries of a task's Activity, of WorkflowCompletedActivity and of a
// split's FetchWorkflowDefinitionActivity, so an error that never clears ends in a park for an admin
// instead of retrying forever. An attempt that
// times out is retried like one that failed. Retries back off from 1s, doubling to a cap of 1h,
// and stop after taskMaxAttempts: about 24h of retrying in all. A non-retryable error parks at once.
var taskRetryPolicy = &temporal.RetryPolicy{
	InitialInterval:    time.Second,
	BackoffCoefficient: 2,
	MaximumInterval:    time.Hour,
	MaximumAttempts:    taskMaxAttempts,
}

// taskMaxAttempts is how many times a task's Activity runs before it parks: 13 attempts doubling from
// 1s to the 1h cap (about 68 minutes), then 23 more an hour apart.
const taskMaxAttempts = 36

func (g *graphInterpreter) handleTaskNode(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge) error {
	inputs, err := g.mapTaskInputs(node.InputMapping)
	if err != nil {
		return err
	}

	// The step may be completed from the moment its Activity starts: a host can complete a step it
	// suspended before the Activity has reported back.
	completion, unregister := g.expectCompletion(ctx, nodeInfo.ActivationID)

	// nodeInfo.ActivationID was minted by the caller (mintActivation, via executeNode or an admin
	// retry) before dispatching here; the Activity is addressed by that same ID, and so is the step's
	// completion, so a stale completion from an earlier run of this node no longer matches.
	nodeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ActivityID:          nodeInfo.ActivationID,
		StartToCloseTimeout: taskActivityTimeout,
		RetryPolicy:         taskRetryPolicy,
	})

	var taskResult TaskResult
	err = workflow.ExecuteActivity(nodeCtx, "ExecuteTaskActivity", node.TaskTemplateID, inputs, g.caseID(),
		ActivationRef{NodeID: node.ID, Seq: nodeInfo.Seq, ManifestVersion: g.manifestVersion()}).Get(ctx, &taskResult)
	result := taskResult.Output
	if err == nil {
		switch {
		case taskResult.Suspended:
			// Wait, outside any Activity, for CompleteActivation to deliver the step's output.
			err = completion.Get(ctx, &result)
		case completion.IsReady():
			// A ForceParkActivation arrived while the Activity was still running; read its error so
			// the node parks instead of ignoring an accepted force-park.
			err = completion.Get(ctx, nil)
		}
	}
	// The step now takes no completion: it completed, failed or was cancelled. This must happen
	// here, not on return, since the node returns only after the rest of the graph has run.
	unregister()
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		return withCategory(ParkCategoryTaskFailure, err)
	}

	// A nil result (the Activity returned nothing) becomes an empty one, so the cache below is
	// non-nil and a park still shows the Activity already ran.
	if result == nil {
		result = map[string]any{}
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
				return withCategory(ParkCategoryGatewayCondition, err)
			}
			if match {
				nodeInfo.Status = NodeStatusCompleted
				nodeInfo.UpdatedAt = workflow.Now(ctx)
				return g.transitionTo(ctx, e)
			}
		}
		return withCategory(ParkCategoryGatewayCondition, fmt.Errorf("no matching conditions found at exclusive gateway %s", node.ID))

	case GatewayTypeParallelSplit:
		return g.handleParallelSplitGateway(ctx, nodeInfo, node, outEdges)

	case GatewayTypeParallelJoin:
		return g.handleParallelJoinGateway(ctx, nodeInfo, node, outEdges)

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
			return withCategory(ParkCategoryDefinitionError, fmt.Errorf("exclusive join %s reached with no incoming token — invalid workflow definition", node.ID))
		}
		if len(outEdges) != 1 {
			return withCategory(ParkCategoryDefinitionError, fmt.Errorf("exclusive join %s must have exactly one outgoing edge, got %d — invalid workflow definition", node.ID, len(outEdges)))
		}
		nodeInfo.Status = NodeStatusCompleted
		nodeInfo.UpdatedAt = workflow.Now(ctx)
		return g.transitionTo(ctx, outEdges[0])

	case GatewayTypeBatchSplit:
		return g.handleBatchSplitGateway(ctx, nodeInfo, node, outEdges)

	case GatewayTypeBatchJoin:
		return g.handleBatchJoinGateway(ctx, nodeInfo, node, outEdges)

	default:
		return withCategory(ParkCategoryDefinitionError, fmt.Errorf("unknown gateway type: %v", node.GatewayType))
	}
}
