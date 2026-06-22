// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/OpenNSW/core/internal/maputil"
)

type activeBranch struct {
	Future   workflow.ChildWorkflowFuture
	BranchID string
	Index    int
}

// handleSplitTaskNode executes the parallel branch fan-out child workflow execution.
func (g *graphInterpreter) handleSplitTaskNode(ctx workflow.Context, nodeInfo *NodeInfo, node *Node, outEdges []Edge) error {
	config := node.SplitTask
	if config == nil {
		return fmt.Errorf("split task configuration is missing on node %s", node.ID)
	}

	// 1. Resolve Items collection from global workflow context
	itemsRaw, exists := maputil.GetNestedKey(g.instance.WorkflowVariables, config.ItemsVariable)
	if !exists {
		return fmt.Errorf("items variable '%s' not found in workflow variables", config.ItemsVariable)
	}

	branchesData, err := resolveBranchesData(itemsRaw, config.ItemsVariable)
	if err != nil {
		return err
	}

	iterKey := config.IterationKey
	if iterKey == "" {
		iterKey = DefaultIterationKey
	}

	// 2. Spawn child workflow interpreters
	activeBranches, err := g.spawnChildWorkflows(ctx, node, config, branchesData, iterKey)
	if err != nil {
		return err
	}

	// 3. Monitor executions and collect outputs/errors
	aggregatedResults := make([]map[string]any, len(activeBranches))
	if err := g.monitorChildWorkflows(ctx, activeBranches, aggregatedResults, config, nodeInfo); err != nil {
		return err
	}

	nodeInfo.Status = NodeStatusCompleted
	nodeInfo.UpdatedAt = workflow.Now(ctx)

	if len(outEdges) > 0 {
		return g.transitionTo(ctx, outEdges[0])
	}
	return nil
}

// resolveBranchesData converts a raw items variable interface into a slice of interface values.
func resolveBranchesData(itemsRaw any, itemsVarName string) ([]any, error) {
	var branchesData []any
	if itemsRaw == nil {
		return nil, nil
	}

	if val, ok := itemsRaw.([]any); ok {
		return val, nil
	}

	// Convert slice of any concrete type to []any
	switch reflectVal := reflect.ValueOf(itemsRaw); reflectVal.Kind() {
	case reflect.Slice:
		branchesData = make([]any, reflectVal.Len())
		for idx := 0; idx < reflectVal.Len(); idx++ {
			branchesData[idx] = reflectVal.Index(idx).Interface()
		}
		return branchesData, nil
	default:
		return nil, fmt.Errorf("items variable '%s' is not a valid list type", itemsVarName)
	}
}

// spawnChildWorkflows spins up child interpreters for all active branches and waits for them to successfully start.
func (g *graphInterpreter) spawnChildWorkflows(
	ctx workflow.Context,
	node *Node,
	config *SplitTaskConfig,
	branchesData []any,
	iterKey string,
) (map[string]*activeBranch, error) {
	parentInfo := workflow.GetInfo(ctx)
	activeBranches := make(map[string]*activeBranch)
	branchIDs := make(map[string]bool)

	type preparedBranch struct {
		TemplateID string
		BranchID   string
		Payload    map[string]any
		Index      int
	}

	prepared := make([]preparedBranch, len(branchesData))
	uniqueTemplates := make([]string, 0)
	seenTemplates := make(map[string]bool)

	// 1. Validate branch IDs and collect unique template IDs
	for i, itemRaw := range branchesData {
		item, err := ParseSplitTaskItem(itemRaw)
		if err != nil {
			return nil, fmt.Errorf("index point %d inside branch resolution array is invalid layout: %w", i, err)
		}

		templateID := node.TaskTemplateID
		if config.Mode == SplitModeDifferentTemplates {
			templateID = item.TemplateID
		}
		branchID := item.BranchID
		if config.Mode == SplitModeSameTemplate {
			branchID = fmt.Sprintf("%s-%d", branchID, i)
		}

		if templateID == "" || branchID == "" {
			return nil, fmt.Errorf("index point %d requires non-empty template_id (static or dynamic) and branch_id configurations", i)
		}

		if _, exists := branchIDs[branchID]; exists {
			return nil, fmt.Errorf("branch ID %s is duplicated", branchID)
		}
		branchIDs[branchID] = true

		prepared[i] = preparedBranch{
			TemplateID: templateID,
			BranchID:   branchID,
			Payload:    item.Payload,
			Index:      i,
		}

		if !seenTemplates[templateID] {
			seenTemplates[templateID] = true
			uniqueTemplates = append(uniqueTemplates, templateID)
		}
	}

	// 2. Fetch unique template definitions in batches in parallel
	const batchSize = 10
	defsMap := make(map[string]WorkflowDefinition)

	for i := 0; i < len(uniqueTemplates); i += batchSize {
		end := i + batchSize
		if end > len(uniqueTemplates) {
			end = len(uniqueTemplates)
		}
		batch := uniqueTemplates[i:end]

		futures := make([]workflow.Future, len(batch))
		for idx, templateID := range batch {
			futures[idx] = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: 15 * time.Second,
			}), "FetchWorkflowDefinitionActivity", templateID)
		}

		for idx, templateID := range batch {
			var branchGraphDef WorkflowDefinition
			if err := futures[idx].Get(ctx, &branchGraphDef); err != nil {
				return nil, fmt.Errorf("boundary lifecycle error hydrating definition graph for template %s: %w", templateID, err)
			}
			defsMap[templateID] = branchGraphDef
		}
	}

	// 3. Spawn child workflow interpreters
	for _, p := range prepared {
		branchGraphDef := defsMap[p.TemplateID]

		childVars := map[string]any{
			VarParentWorkflowID: parentInfo.WorkflowExecution.ID,
			VarSplitNodeID:      node.ID,
			VarBranchID:         p.BranchID,
			iterKey: map[string]any{
				IterIndexKey:    p.Index,
				IterBranchIDKey: p.BranchID,
				IterInputKey:    p.Payload,
			},
		}

		deterministicChildID := FormatChildWorkflowID(parentInfo.WorkflowExecution.ID, node.ID, p.BranchID)
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: deterministicChildID,
		})

		// Recursively spin up core interpreter executing target hydrated sub-graph schema
		future := workflow.ExecuteChildWorkflow(childCtx, "GraphInterpreterWorkflow", branchGraphDef, childVars)
		activeBranches[deterministicChildID] = &activeBranch{
			Future:   future,
			BranchID: p.BranchID,
			Index:    p.Index,
		}
	}

	// Wait for all child workflows to start to ensure their execution environments (and signal handlers) are initialized.
	// Iterate prepared (insertion order) rather than the map to guarantee deterministic replay.
	for _, p := range prepared {
		childID := FormatChildWorkflowID(parentInfo.WorkflowExecution.ID, node.ID, p.BranchID)
		var childExec workflow.Execution
		if err := activeBranches[childID].Future.GetChildWorkflowExecution().Get(ctx, &childExec); err != nil {
			return nil, fmt.Errorf("failed to start child workflow %s: %w", childID, err)
		}
	}

	return activeBranches, nil
}

// monitorChildWorkflows tracks and waits for spawned child workflows, handles cross-branch signaling, and collects outputs/errors.
func (g *graphInterpreter) monitorChildWorkflows(
	ctx workflow.Context,
	activeBranches map[string]*activeBranch,
	aggregatedResults []map[string]any,
	config *SplitTaskConfig,
	nodeInfo *NodeInfo,
) error {
	// 3. Initialize the Interactive Event Multiplexor Selector
	broadcastChan := workflow.GetSignalChannel(ctx, ChildBroadcastSignalName)
	selector := workflow.NewSelector(ctx)

	// Handler Type A: Listen for Upstream Child Broadcast signals and multicast them out
	selector.AddReceive(broadcastChan, func(c workflow.ReceiveChannel, _ bool) {
		var msg BroadcastMessage
		c.Receive(ctx, &msg)

		// Broadcast packet down into active sibling tracking channels (omitting source generator).
		// Sort child IDs before iterating so SignalExternalWorkflow commands are scheduled in
		// deterministic order on both the original run and replay.
		childIDs := make([]string, 0, len(activeBranches))
		for childID := range activeBranches {
			childIDs = append(childIDs, childID)
		}
		sort.Strings(childIDs)
		for _, targetChildID := range childIDs {
			if activeBranches[targetChildID].BranchID != msg.SenderBranchID {
				_ = workflow.SignalExternalWorkflow(ctx, targetChildID, "", msg.SignalName, msg.Payload)
			}
		}
	})

	completedCount := 0
	totalBranches := len(activeBranches)
	var executionError error
	var failedBranchesErrors []error

	// 4. Register active sub-workflow execution handles on monitoring tracking hooks
	for cid, info := range activeBranches {
		targetID := cid
		branchInfo := info

		selector.AddFuture(branchInfo.Future, func(wf workflow.Future) {
			var childOutput *WorkflowInstance
			err := wf.Get(ctx, &childOutput)

			if err != nil {
				executionError = fmt.Errorf("dynamic execution track %s halted abnormally: %w", targetID, err)
				failedBranchesErrors = append(failedBranchesErrors, executionError)
				aggregatedResults[branchInfo.Index] = map[string]any{
					"error":     err.Error(),
					"branch_id": branchInfo.BranchID,
				}
			} else if childOutput != nil {
				// Collect results chronologically mapped back to setup registry indexes
				aggregatedResults[branchInfo.Index] = childOutput.WorkflowVariables
			}

			delete(activeBranches, targetID)
			completedCount++
		})
	}

	// 5. Block Execution thread loop until all tracks successfully resolve
	for completedCount < totalBranches {
		selector.Select(ctx) // Suspends workflow thread awaiting state activation triggers cleanly

		if executionError != nil && config.FailureMode == FailureModeFailFast {
			nodeInfo.Status = NodeStatusFailed
			remaining := make([]string, 0, len(activeBranches))
			for childID := range activeBranches {
				remaining = append(remaining, childID)
			}
			sort.Strings(remaining)
			for _, childID := range remaining {
				_ = workflow.RequestCancelExternalWorkflow(ctx, childID, "")
			}
			return executionError
		}
	}

	// 6. Commit variable mutation changes back to the primary context map layer
	if config.ResultsVariable != "" {
		maputil.SetNestedKey(g.instance.WorkflowVariables, config.ResultsVariable, aggregatedResults)
	}

	if len(failedBranchesErrors) > 0 && config.FailureMode == FailureModeCollectAll {
		nodeInfo.Status = NodeStatusFailed
		logger := workflow.GetLogger(ctx)
		for _, e := range failedBranchesErrors {
			logger.Error("Split task branch execution failure", "error", e.Error())
		}
		var errMsgs []string
		for _, e := range failedBranchesErrors {
			errMsgs = append(errMsgs, e.Error())
		}
		return fmt.Errorf("multiple branches failed: [%s]", strings.Join(errMsgs, "; "))
	}

	return nil
}
