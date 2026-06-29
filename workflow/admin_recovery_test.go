// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// TestAdminOverrideResolvesInputMappingError parks on a missing input mapping, then resolves
// it with AdminActionOverride. Override never re-runs the node's own logic, so the Activity
// is not expected to be invoked.
func TestAdminOverrideResolvesInputMappingError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "task",
			Action: AdminActionOverride,
			Reason: "supplying the missing value directly",
		})
	}, time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{
		"global_user_email": "user@example.com",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Equal(t, StatusCompleted, instance.Status)
	require.Equal(t, NodeStatusCompleted, instance.NodeInfo["task"].Status)
	require.Empty(t, instance.NodeInfo["task"].LastError)

	env.AssertExpectations(t)
	env.AssertNotCalled(t, "ExecuteTaskActivity", mock.Anything, "TASK_WITH_MISSING_INPUT", mock.Anything)
}

// TestAdminRetryResolvesInputMappingError parks on a missing input mapping, then resolves it
// with AdminActionRetry supplying the missing variable as an override. Since the Activity
// never ran before the park (the failure was in input mapping), retrying re-runs the whole
// node from scratch and the Activity is invoked exactly once.
func TestAdminRetryResolvesInputMappingError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_WITH_MISSING_INPUT", mock.Anything).
		Return(map[string]any{}, nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:    "task",
			Action:    AdminActionRetry,
			Overrides: map[string]any{"missing_global_var": "fixed-value"},
		})
	}, time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{
		"global_user_email": "user@example.com",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Equal(t, StatusCompleted, instance.Status)
	require.Equal(t, NodeStatusCompleted, instance.NodeInfo["task"].Status)
	require.Equal(t, "fixed-value", instance.WorkflowVariables["missing_global_var"])

	env.AssertExpectations(t)
}

// TestAdminOverrideResolvesOutputMappingErrorWithoutReinvokingActivity parks on a missing
// output mapping key — meaning the Activity already ran successfully. Resolving with
// AdminActionOverride must supply the value directly without re-invoking the Activity again.
func TestAdminOverrideResolvesOutputMappingErrorWithoutReinvokingActivity(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingRequiredOutputWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_MISSING_REQUIRED_OUTPUT", mock.Anything).
		Return(map[string]any{}, nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	// Confirm the parked LastError explicitly warns that the Activity already ran, before
	// resolving it.
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		require.Contains(t, instance.NodeInfo["task"].LastError, "already completed successfully")
		require.Contains(t, instance.NodeInfo["task"].LastError, "use OVERRIDE instead of RETRY")
	}, time.Millisecond)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:    "task",
			Action:    AdminActionOverride,
			Overrides: map[string]any{"global_user_phone": "555-1234"},
		})
	}, 2*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Equal(t, StatusCompleted, instance.Status)
	require.Equal(t, "555-1234", instance.WorkflowVariables["global_user_phone"])
	require.Empty(t, instance.NodeInfo["task"].CachedTaskResult)

	// .Once() above already enforces this, but AssertExpectations makes the intent explicit:
	// the Activity that already ran must not be invoked a second time by the override.
	env.AssertExpectations(t)
}

// TestAdminRetryRefreshesCachedTaskResultWithoutStaleData parks on an output mapping error
// (Activity already ran), then resolves with Retry without fixing the underlying problem —
// the Activity runs a second time and the same output mapping error parks the node again.
// CachedTaskResult and the "already ran" warning must reflect this latest attempt, not linger
// from the first one.
func TestAdminRetryRefreshesCachedTaskResultWithoutStaleData(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingRequiredOutputWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_MISSING_REQUIRED_OUTPUT", mock.Anything).
		Return(map[string]any{"attempt": "first"}, nil).Once()
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_MISSING_REQUIRED_OUTPUT", mock.Anything).
		Return(map[string]any{"attempt": "second"}, nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	// Retry without fixing anything — the Activity runs again, output mapping fails again.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "task",
			Action: AdminActionRetry,
		})
	}, time.Millisecond)

	// Confirm the re-park reflects this latest attempt, not a stale leftover from the first.
	// (100ms gives the second Activity invocation time to actually complete in the test
	// environment before we query — same timing characteristic seen elsewhere in this suite.)
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		require.Equal(t, NodeStatusAwaitingAdmin, instance.NodeInfo["task"].Status)
		require.Equal(t, "second", instance.NodeInfo["task"].CachedTaskResult["attempt"])
		require.Contains(t, instance.NodeInfo["task"].LastError, "already completed successfully")
	}, 100*time.Millisecond)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:    "task",
			Action:    AdminActionOverride,
			Overrides: map[string]any{"global_user_phone": "555-1234"},
		})
	}, 200*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestAdminSkipContinuesPastParkedNode resolves a parked node with AdminActionSkip: no
// variables are set, but the graph still continues past it to the END node.
func TestAdminSkipContinuesPastParkedNode(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "task",
			Action: AdminActionSkip,
		})
	}, time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{
		"global_user_email": "user@example.com",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Equal(t, StatusCompleted, instance.Status)
	require.Equal(t, NodeStatusCompleted, instance.NodeInfo["task"].Status)
	require.NotContains(t, instance.WorkflowVariables, "local_key")

	// END node's WorkflowCompletedActivity firing proves execution continued past the skip.
	env.AssertExpectations(t)
}

// TestAdminResolutionUnknownNodeIDAndMalformedActionAreNoOps verifies that a resolution
// signal targeting a NodeID nobody is waiting on, and a signal with a garbage Action, are
// both silently ignored rather than failing the node — only a deliberate, well-formed
// resolution should ever move a parked node forward.
func TestAdminResolutionUnknownNodeIDAndMalformedActionAreNoOps(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "no-such-node",
			Action: AdminActionAbort,
		})
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "task",
			Action: "NOT_A_REAL_ACTION",
		})
	}, 2*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "task",
			Action: AdminActionAbort,
		})
	}, 3*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{
		"global_user_email": "user@example.com",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Contains(t, env.GetWorkflowError().Error(), "input mapping error")
}

const gatewayParkWorkflowJSON = `
{
  "id": "gateway-park-test",
  "name": "Gateway Park Test",
  "version": 1,
  "edges": [
    { "id": "e1", "source_id": "start", "target_id": "gateway" },
    { "id": "e2", "source_id": "gateway", "target_id": "task_pass", "condition": "decision == \"pass\"" },
    { "id": "e3", "source_id": "task_pass", "target_id": "end" }
  ],
  "nodes": [
    { "id": "start", "type": "START" },
    { "id": "gateway", "type": "GATEWAY", "gateway_type": "EXCLUSIVE_SPLIT" },
    { "id": "task_pass", "type": "TASK", "task_template_id": "TASK_PASS" },
    { "id": "end", "type": "END" }
  ]
}`

// TestAdminSkipAndOverrideRejectedForParkedGatewayNode proves that a parked GATEWAY node
// cannot be resolved with Skip or Override (both would bypass the gateway's real routing
// logic by blindly taking its first outgoing edge) — only Retry (after correcting the
// missing variable) or Abort are accepted.
func TestAdminSkipAndOverrideRejectedForParkedGatewayNode(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(gatewayParkWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_PASS", mock.Anything).
		Return(map[string]any{}, nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	// Skip: rejected, node stays parked.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "gateway",
			Action: AdminActionSkip,
		})
	}, time.Millisecond)

	// Override: rejected, node stays parked.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:    "gateway",
			Action:    AdminActionOverride,
			Overrides: map[string]any{"decision": "pass"},
		})
	}, 2*time.Millisecond)

	// Confirm it's still parked after both rejections.
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		require.Equal(t, NodeStatusAwaitingAdmin, instance.NodeInfo["gateway"].Status)
	}, 3*time.Millisecond)

	// Retry with the corrected variable: the gateway re-evaluates its real condition and
	// correctly routes to task_pass.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:    "gateway",
			Action:    AdminActionRetry,
			Overrides: map[string]any{"decision": "pass"},
		})
	}, 4*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Equal(t, StatusCompleted, instance.Status)
	require.Equal(t, NodeStatusCompleted, instance.NodeInfo["gateway"].Status)

	env.AssertExpectations(t)
}

// TestAdminParkingIsolatesParallelBranches proves that one branch's parked node does not
// block a sibling branch running in parallel: the sibling completes while the first branch
// is still NodeStatusAwaitingAdmin. Aborting the parked branch afterward still fails the
// overall workflow, since parallel join semantics are unchanged.
func TestAdminParkingIsolatesParallelBranches(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(parallelWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_A", mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("boom", "TaskFailure", nil)).Once()
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_B", mock.Anything).
		Return(map[string]any{}, nil).Once()

	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))

		require.Equal(t, NodeStatusAwaitingAdmin, instance.NodeInfo["task_a"].Status)
		require.Equal(t, NodeStatusCompleted, instance.NodeInfo["task_b"].Status)
	}, time.Second)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "task_a",
			Action: AdminActionAbort,
		})
	}, 2*time.Second)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Contains(t, env.GetWorkflowError().Error(), "boom")

	env.AssertExpectations(t)
	env.AssertNotCalled(t, "ExecuteTaskActivity", mock.Anything, "TASK_C", mock.Anything)
}
