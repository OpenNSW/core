// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestWithCategoryAndCategoryOf(t *testing.T) {
	require.NoError(t, withCategory(ParkCategoryTaskFailure, nil))
	require.Equal(t, ParkCategoryUnknown, categoryOf(errors.New("plain")))
	require.Equal(t, ParkCategoryUnknown, categoryOf(nil))

	base := errors.New("boom")
	tagged := withCategory(ParkCategoryTaskFailure, base)
	require.Equal(t, ParkCategoryTaskFailure, categoryOf(tagged))
	require.Equal(t, "boom", tagged.Error())
	require.ErrorIs(t, tagged, base)

	// The category survives wrapping by callers further up the stack.
	require.Equal(t, ParkCategoryTaskFailure, categoryOf(fmt.Errorf("node x: %w", tagged)))

	// The innermost tag wins: re-tagging an already tagged error is a no-op.
	require.Equal(t, ParkCategoryTaskFailure, categoryOf(withCategory(ParkCategoryChildFailure, tagged)))
}

func newMappingTestInterpreter(vars map[string]any) *graphInterpreter {
	return &graphInterpreter{instance: &WorkflowInstance{WorkflowVariables: vars}}
}

func TestMapTaskOutputsIsAtomicAndReportsAllMissingSorted(t *testing.T) {
	vars := map[string]any{"existing": "kept"}
	g := newMappingTestInterpreter(vars)

	err := g.mapTaskOutputs(vars,
		map[string]string{
			"zeta":  "g.zeta",
			"alpha": "g.alpha",
			"ok":    "g.ok",
			"mid":   "g.mid",
		},
		map[string]any{"ok": "present"},
	)

	require.Error(t, err)
	require.Equal(t, ParkCategoryOutputMapping, categoryOf(err))
	require.Contains(t, err.Error(), "required task variables 'alpha', 'mid', 'zeta' not found in task result")
	// Nothing was written, not even the key that could have been mapped.
	require.Equal(t, map[string]any{"existing": "kept"}, vars)
}

func TestMapTaskOutputsSingleMissingKeyKeepsSingularMessage(t *testing.T) {
	vars := map[string]any{}
	g := newMappingTestInterpreter(vars)

	err := g.mapTaskOutputs(vars, map[string]string{"phone": "user.phone"}, map[string]any{})

	require.Error(t, err)
	require.Contains(t, err.Error(), "required task variable 'phone' not found in task result")
	require.Empty(t, vars)
}

func TestMapTaskOutputsOptionalKeysAreSkippedNotReported(t *testing.T) {
	vars := map[string]any{}
	g := newMappingTestInterpreter(vars)

	err := g.mapTaskOutputs(vars,
		map[string]string{"nickname?": "user.nickname", "name": "user.name"},
		map[string]any{"name": "Nimal"},
	)

	require.NoError(t, err)
	require.Equal(t, map[string]any{"user": map[string]any{"name": "Nimal"}}, vars)
}

func TestMapTaskOutputsTreatsNilResultAsEmpty(t *testing.T) {
	vars := map[string]any{"existing": "kept"}
	g := newMappingTestInterpreter(vars)

	err := g.mapTaskOutputs(vars, map[string]string{"zeta": "g.zeta", "alpha": "g.alpha", "opt?": "g.opt"}, nil)

	require.Error(t, err)
	require.Equal(t, ParkCategoryOutputMapping, categoryOf(err))
	require.Contains(t, err.Error(), "required task variables 'alpha', 'zeta' not found in task result")
	require.Equal(t, map[string]any{"existing": "kept"}, vars)

	// Only optional keys: nothing required is missing, so a nil result is fine.
	require.NoError(t, g.mapTaskOutputs(vars, map[string]string{"opt?": "g.opt"}, nil))
	require.Equal(t, map[string]any{"existing": "kept"}, vars)
}

func TestMapTaskOutputsWritesEverythingOnSuccess(t *testing.T) {
	vars := map[string]any{}
	g := newMappingTestInterpreter(vars)

	err := g.mapTaskOutputs(vars,
		map[string]string{"a": "g.a", "b.c": "g.bc"},
		map[string]any{"a": 1, "b": map[string]any{"c": 2}},
	)

	require.NoError(t, err)
	require.Equal(t, map[string]any{"g": map[string]any{"a": 1, "bc": 2}}, vars)
}

func TestMapTaskInputsReportsAllMissingSortedAndIsDeterministic(t *testing.T) {
	g := newMappingTestInterpreter(map[string]any{"present": "x"})
	mapping := map[string]string{
		"zeta":      "z",
		"alpha":     "a",
		"present":   "p",
		"optional?": "o",
		"mid":       "m",
	}

	// Map iteration order is random, so repeat to make a nondeterministic message very likely
	// to show up.
	for range 50 {
		inputs, err := g.mapTaskInputs(mapping)
		require.Nil(t, inputs)
		require.Error(t, err)
		require.Equal(t, ParkCategoryInputMapping, categoryOf(err))
		require.Contains(t, err.Error(), "required global variables 'alpha', 'mid', 'zeta' not found in workflow variables")
	}
}

func TestMapTaskInputsSingleMissingKeyKeepsSingularMessage(t *testing.T) {
	g := newMappingTestInterpreter(map[string]any{})

	_, err := g.mapTaskInputs(map[string]string{"missing_global_var": "local_key"})

	require.Error(t, err)
	require.Contains(t, err.Error(), "required global variable 'missing_global_var' not found in workflow variables")
}

// parkAndInspect runs def until nodeID parks for admin, captures its NodeInfo, then aborts the
// node so the workflow terminates. setup registers the Activity mocks the scenario needs.
func parkAndInspect(t *testing.T, def WorkflowDefinition, vars map[string]any, nodeID string, setup func(env *testsuite.TestWorkflowEnvironment)) NodeInfo {
	t.Helper()

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	if setup != nil {
		setup(env)
	}

	var parked *NodeInfo
	// 100ms rather than 1ms: for an output mapping error the Activity must finish before the
	// park records anything, and a shorter delay races it.
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		parked = instance.NodeInfo[nodeID]
	}, 100*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{NodeID: nodeID, Action: AdminActionAbort})
	}, 200*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, vars)
	require.True(t, env.IsWorkflowCompleted())
	require.NotNil(t, parked, "node %q never appeared in GetStatus", nodeID)
	return *parked
}

func mustParseDefinition(t *testing.T, raw string) WorkflowDefinition {
	t.Helper()
	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(raw), &def))
	return def
}

const timerWithoutConfigWorkflowJSON = `
{
  "id": "timer-without-config",
  "name": "Timer Without Config",
  "version": 1,
  "edges": [
    { "id": "e1", "source_id": "start", "target_id": "wait" },
    { "id": "e2", "source_id": "wait", "target_id": "end" }
  ],
  "nodes": [
    { "id": "start", "type": "START" },
    { "id": "wait", "type": "TIMER" },
    { "id": "end", "type": "END" }
  ]
}`

func TestParkCategoryIsRecordedForEachFailureKind(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		vars     map[string]any
		nodeID   string
		setup    func(env *testsuite.TestWorkflowEnvironment)
		category ParkCategory
	}{
		{
			name:     "missing input mapping variable",
			raw:      missingInputMappingKeyWorkflowJSON,
			vars:     map[string]any{},
			nodeID:   "task",
			category: ParkCategoryInputMapping,
		},
		{
			name:   "task result lacks a mapped field",
			raw:    missingRequiredOutputWorkflowJSON,
			vars:   map[string]any{},
			nodeID: "task",
			setup: func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_MISSING_REQUIRED_OUTPUT", mock.Anything, mock.Anything).
					Return(map[string]any{}, nil).Once()
			},
			category: ParkCategoryOutputMapping,
		},
		{
			name:   "task activity fails",
			raw:    missingRequiredOutputWorkflowJSON,
			vars:   map[string]any{},
			nodeID: "task",
			setup: func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_MISSING_REQUIRED_OUTPUT", mock.Anything, mock.Anything).
					Return(nil, temporal.NewNonRetryableApplicationError("boom", "TaskFailure", nil)).Once()
			},
			category: ParkCategoryTaskFailure,
		},
		{
			name:     "gateway condition cannot be evaluated",
			raw:      gatewayParkWorkflowJSON,
			vars:     map[string]any{},
			nodeID:   "gateway",
			category: ParkCategoryGatewayCondition,
		},
		{
			name:     "gateway condition matches no edge",
			raw:      gatewayParkWorkflowJSON,
			vars:     map[string]any{"decision": "fail"},
			nodeID:   "gateway",
			category: ParkCategoryGatewayCondition,
		},
		{
			name:     "definition is invalid",
			raw:      timerWithoutConfigWorkflowJSON,
			vars:     map[string]any{},
			nodeID:   "wait",
			category: ParkCategoryDefinitionError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			def := mustParseDefinition(t, tc.raw)
			parked := parkAndInspect(t, def, tc.vars, tc.nodeID, tc.setup)

			require.Equal(t, NodeStatusAwaitingAdmin, parked.Status)
			require.NotEmpty(t, parked.LastError)
			require.Equal(t, tc.category, parked.ParkCategory)
		})
	}
}

func TestParkCategorySplitDataForBatchSplitWithoutItems(t *testing.T) {
	def := WorkflowDefinition{
		ID:   "batch_missing_items",
		Name: "Batch Missing Items",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{ItemsVariable: "commodities", IDField: "commodity_id"}},
			{ID: "food", Type: NodeTypeTask, TaskTemplateID: "FOOD"},
			{ID: "join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw", ItemsVariable: "commodities", IDField: "commodity_id"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw"},
			{ID: "e2", SourceID: "gw", TargetID: "food", Condition: `item.type == "food"`},
			{ID: "e3", SourceID: "food", TargetID: "join"},
			{ID: "e4", SourceID: "join", TargetID: "end"},
		},
	}

	parked := parkAndInspect(t, def, map[string]any{}, "gw", nil)

	require.Equal(t, NodeStatusAwaitingAdmin, parked.Status)
	require.Contains(t, parked.LastError, "not found in workflow variables")
	require.Equal(t, ParkCategorySplitData, parked.ParkCategory)
}

// TestParkCategoryOfNodeInsideParallelBranch checks that a node parked inside a parallel branch
// records its own category (TASK_FAILURE) in the branch's status. Aborting it is a terminal admin
// error, so the parent's PARALLEL_SPLIT deliberately does not park again; CHILD_FAILURE is only
// for a child that fails without having gone through an admin park.
func TestParkCategoryOfNodeInsideParallelBranch(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	def := mustParseDefinition(t, parallelWorkflowJSON)

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_A", mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("boom", "TaskFailure", nil)).Once()
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_B", mock.Anything, mock.Anything).
		Return(map[string]any{}, nil).Once()

	branchWorkflowID := FormatChildWorkflowID("default-test-workflow-id", "default-test-workflow-id", "split", "e2")

	var branchNode *NodeInfo
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflowByID(branchWorkflowID, "GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		branchNode = instance.NodeInfo["task_a"]
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		require.NoError(t, env.SignalWorkflowByID(branchWorkflowID, AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "task_a",
			Action: AdminActionAbort,
		}))
	}, 2*time.Second)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())

	require.NotNil(t, branchNode)
	require.Equal(t, NodeStatusAwaitingAdmin, branchNode.Status)
	require.Equal(t, ParkCategoryTaskFailure, branchNode.ParkCategory)
}

func TestParkCategoryIsClearedOnceResolved(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	def := mustParseDefinition(t, missingInputMappingKeyWorkflowJSON)

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_WITH_MISSING_INPUT", mock.Anything, mock.Anything).
		Return(map[string]any{}, nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:                 "task",
			Action:                 AdminActionRetry,
			WorkflowVariablesPatch: map[string]any{"missing_global_var": "fixed"},
		})
	}, time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Empty(t, instance.NodeInfo["task"].ParkCategory)
	require.Empty(t, instance.NodeInfo["task"].InputMapping)
	require.Empty(t, instance.NodeInfo["task"].OutputMapping)
}

// TestNodeInfoMappingsAreOnlySetWhileParked checks that mappings are park context, not something
// every node carries: a node with mappings that never parks reports none.
func TestNodeInfoMappingsAreOnlySetWhileParked(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	def := mustParseDefinition(t, inputMappingWorkflowJSON)
	require.NotEmpty(t, def.Nodes[1].InputMapping, "fixture must have a node with an input mapping")

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_INPUTS", mock.Anything, mock.Anything).
		Return(map[string]any{}, nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{"global_user_email": "user@example.com"})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	for id, info := range instance.NodeInfo {
		require.Empty(t, info.InputMapping, "node %s", id)
		require.Empty(t, info.OutputMapping, "node %s", id)
	}
}

func TestNodeInfoCarriesMappingsForParkedTask(t *testing.T) {
	def := mustParseDefinition(t, missingInputMappingKeyWorkflowJSON)
	parked := parkAndInspect(t, def, map[string]any{}, "task", nil)

	require.Equal(t, map[string]string{"missing_global_var": "local_key"}, parked.InputMapping)
	require.Empty(t, parked.OutputMapping)

	def = mustParseDefinition(t, missingRequiredOutputWorkflowJSON)
	parked = parkAndInspect(t, def, map[string]any{}, "task", func(env *testsuite.TestWorkflowEnvironment) {
		env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_MISSING_REQUIRED_OUTPUT", mock.Anything, mock.Anything).
			Return(map[string]any{}, nil).Once()
	})

	require.Equal(t, map[string]string{"task_phone": "global_user_phone"}, parked.OutputMapping)
	require.Empty(t, parked.InputMapping)
}

// TestNilActivityResultWithRequiredMappingParks: an Activity that returns nothing must not let a
// node with required output mappings complete, and the park must still say the Activity already ran.
func TestNilActivityResultWithRequiredMappingParks(t *testing.T) {
	def := mustParseDefinition(t, missingRequiredOutputWorkflowJSON)
	parked := parkAndInspect(t, def, map[string]any{}, "task", func(env *testsuite.TestWorkflowEnvironment) {
		env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_MISSING_REQUIRED_OUTPUT", mock.Anything, mock.Anything).
			Return(nil, nil).Once()
	})

	require.Equal(t, NodeStatusAwaitingAdmin, parked.Status)
	require.Equal(t, ParkCategoryOutputMapping, parked.ParkCategory)
	require.Contains(t, parked.LastError, "required task variable 'task_phone' not found in task result")
	require.Contains(t, parked.LastError, "already completed successfully")
}

// TestNullSignalPayloadWithRequiredMappingParks: a WAIT node whose signal carries a null payload
// fails its required output mapping like any other missing field, and the park still shows the
// signal already arrived.
func TestNullSignalPayloadWithRequiredMappingParks(t *testing.T) {
	const waitSignalJSON = `
	{
		"id": "wait-null-signal",
		"name": "Wait Null Signal",
		"version": 1,
		"edges": [
			{ "id": "e1", "source_id": "start", "target_id": "wait" },
			{ "id": "e2", "source_id": "wait", "target_id": "end" }
		],
		"nodes": [
			{ "id": "start", "type": "START" },
			{ "id": "wait", "type": "SIGNALING", "signaling": { "type": "WAIT", "signal_name": "my_test_signal" },
			  "output_mapping": { "needed": "global_target" } },
			{ "id": "end", "type": "END" }
		]
	}`
	def := mustParseDefinition(t, waitSignalJSON)

	parked := parkAndInspect(t, def, map[string]any{}, "wait", func(env *testsuite.TestWorkflowEnvironment) {
		env.RegisterDelayedCallback(func() { env.SignalWorkflow("my_test_signal", nil) }, time.Millisecond)
	})

	require.Equal(t, NodeStatusAwaitingAdmin, parked.Status)
	require.Equal(t, ParkCategoryOutputMapping, parked.ParkCategory)
	require.Contains(t, parked.LastError, "required task variable 'needed' not found in task result")
	require.Contains(t, parked.LastError, "already completed successfully")
}

// TestRetryOnParkedSignalWaitWaitsForANewSignal documents that RETRY discards the signal a WAIT
// node already received and waits for a new one, which then maps normally.
func TestRetryOnParkedSignalWaitWaitsForANewSignal(t *testing.T) {
	const waitSignalJSON = `
	{
		"id": "wait-retry",
		"name": "Wait Retry",
		"version": 1,
		"edges": [
			{ "id": "e1", "source_id": "start", "target_id": "wait" },
			{ "id": "e2", "source_id": "wait", "target_id": "end" }
		],
		"nodes": [
			{ "id": "start", "type": "START" },
			{ "id": "wait", "type": "SIGNALING", "signaling": { "type": "WAIT", "signal_name": "my_test_signal" },
			  "output_mapping": { "needed": "global_target" } },
			{ "id": "end", "type": "END" }
		]
	}`
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	def := mustParseDefinition(t, waitSignalJSON)

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	var afterRetry *NodeInfo
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("my_test_signal", map[string]any{"incorrect_key": "value"})
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{NodeID: "wait", Action: AdminActionRetry})
	}, 100*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		afterRetry = instance.NodeInfo["wait"]
	}, 150*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("my_test_signal", map[string]any{"needed": "corrected"})
	}, 200*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	require.NotNil(t, afterRetry)
	require.Equal(t, NodeStatusRunning, afterRetry.Status, "the retried node waits for a new signal")

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Equal(t, "corrected", instance.WorkflowVariables["global_target"])
}

// TestSignalingEmitReportsAllMissingInputsSorted: an EMIT node with several missing required input
// mappings reports all of them in sorted order, on every run.
func TestSignalingEmitReportsAllMissingInputsSorted(t *testing.T) {
	const emitJSON = `
	{
		"id": "emit-missing-inputs",
		"name": "Emit Missing Inputs",
		"version": 1,
		"edges": [
			{ "id": "e1", "source_id": "start", "target_id": "emit" },
			{ "id": "e2", "source_id": "emit", "target_id": "end" }
		],
		"nodes": [
			{ "id": "start", "type": "START" },
			{ "id": "emit", "type": "SIGNALING", "signaling": { "type": "EMIT", "signal_name": "s" },
			  "input_mapping": { "zeta": "z", "alpha": "a", "present": "p", "optional?": "o", "mid": "m" } },
			{ "id": "end", "type": "END" }
		]
	}`
	def := mustParseDefinition(t, emitJSON)

	for range 5 {
		parked := parkAndInspect(t, def, map[string]any{VarParentWorkflowID: "parent", "present": "x"}, "emit", nil)

		require.Equal(t, NodeStatusAwaitingAdmin, parked.Status)
		require.Equal(t, ParkCategoryInputMapping, parked.ParkCategory)
		require.Contains(t, parked.LastError, "required global variables 'alpha', 'mid', 'zeta' not found")
	}
}
