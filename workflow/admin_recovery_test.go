// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// parkedActivationID queries the running workflow's GetStatus and returns nodeID's current
// ActivationID, so a test can resolve a parked node without guessing it — ActivationID is the
// routing key AdminResolutionSignal must supply. A query reflects the workflow's already-committed
// deterministic state (NodeInfo.ActivationID is set as part of the same workflow task that parks
// the node), so unlike reading AdminParkActivity's own payload, this isn't a race against that
// Activity's real, asynchronous dispatch.
func parkedActivationID(t *testing.T, env *testsuite.TestWorkflowEnvironment, nodeID string) string {
	t.Helper()
	val, err := env.QueryWorkflow("GetStatus")
	require.NoError(t, err)
	var instance WorkflowInstance
	require.NoError(t, val.Get(&instance))
	return instance.NodeInfo[nodeID].ActivationID
}

// An admin tool reads the ActivationID from GetStatus (NodeInfo) and sends it back in
// AdminResolutionSignal, so both must use the same JSON name.
func TestAdminResolutionSignalUsesNodeInfoJSONNameForActivationID(t *testing.T) {
	info, err := json.Marshal(NodeInfo{ActivationID: "a1"})
	require.NoError(t, err)
	sig, err := json.Marshal(AdminResolutionSignal{ActivationID: "a1"})
	require.NoError(t, err)
	require.Contains(t, string(info), `"step_id":"a1"`)
	require.Contains(t, string(sig), `"step_id":"a1"`)
}

func TestApplyVariablesPatchIsDeterministicForOverlappingKeys(t *testing.T) {
	// "a" is written first and "a.y" inside it. Applied in sorted order the parent always goes
	// first, so both survive on every run.
	patch := map[string]any{
		"a":   map[string]any{"x": 1},
		"a.y": 2,
	}
	for range 50 {
		vars := map[string]any{}
		applyVariablesPatch(vars, patch)
		require.Equal(t, map[string]any{"a": map[string]any{"x": 1, "y": 2}}, vars)
	}
}

// TestApplyVariablesPatchMergesMapsAndReplacesOtherValues pins the write semantics the
// WorkflowVariablesPatch comment describes.
func TestApplyVariablesPatchMergesMapsAndReplacesOtherValues(t *testing.T) {
	vars := map[string]any{
		"review": map[string]any{"legacy": true, "outcome": "pending"},
		"status": map[string]any{"code": 1},
		"count":  1,
	}

	applyVariablesPatch(vars, map[string]any{
		"review": map[string]any{"outcome": "approved"}, // map over map: merged, "legacy" kept
		"status": "done",                                // scalar over map: replaced
		"count":  2,
	})

	require.Equal(t, map[string]any{
		"review": map[string]any{"legacy": true, "outcome": "approved"},
		"status": "done",
		"count":  2,
	}, vars)
}

// TestApplyVariablesPatchNilThenKeysDropsOtherFields covers the documented way to drop a map's
// other fields: a nil for the map plus the fields to keep, which sorted order applies parent first.
func TestApplyVariablesPatchNilThenKeysDropsOtherFields(t *testing.T) {
	for range 20 {
		vars := map[string]any{"review": map[string]any{"legacy": true, "outcome": "pending"}}

		applyVariablesPatch(vars, map[string]any{"review": nil, "review.outcome": true})

		require.Equal(t, map[string]any{"review": map[string]any{"outcome": true}}, vars)
	}

	// An empty map merges, so it clears nothing.
	vars := map[string]any{"review": map[string]any{"legacy": true}}
	applyVariablesPatch(vars, map[string]any{"review": map[string]any{}})
	require.Equal(t, map[string]any{"review": map[string]any{"legacy": true}}, vars)
}

// TestAdminCompleteResolvesInputMappingError parks on a missing input mapping, then resolves
// it with AdminActionComplete. Complete never runs the node's own logic, so the Activity
// is not expected to be invoked.
func TestAdminCompleteResolvesInputMappingError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: parkedActivationID(t, env, "task"),
			Action:       AdminActionComplete,
			Reason:       "supplying the missing value directly",
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
	env.AssertNotCalled(t, "ExecuteTaskActivity", mock.Anything, "TASK_WITH_MISSING_INPUT", mock.Anything, mock.Anything, mock.Anything)
}

// TestAdminParkNotifiesHostAppOnFreshExecution pins that parking a node actually invokes
// AdminParkActivity. Every other park test in this file registers the activity without asserting
// it's called, so on its own none of them would catch notifyAdminPark silently not being invoked.
func TestAdminParkNotifiesHostAppOnFreshExecution(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("AdminParkActivity", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: parkedActivationID(t, env, "task"),
			Action:       AdminActionComplete,
			Reason:       "supplying the missing value directly",
		})
	}, time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{
		"global_user_email": "user@example.com",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestAdminParkNotificationCarriesCategoryAndMappings pins that AdminParkPayload gives the host
// app the same park context NodeInfo carries — ParkCategory, InputMapping and OutputMapping —
// not just Cause, so a handler (paging, logging, persistence) doesn't have to re-derive them.
func TestAdminParkNotificationCarriesCategoryAndMappings(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})

	var captured AdminParkPayload
	env.OnActivity("AdminParkActivity", mock.Anything, mock.Anything).Return(
		func(_ context.Context, payload AdminParkPayload) error {
			captured = payload
			return nil
		}).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	var parkedID string
	env.RegisterDelayedCallback(func() {
		parkedID = parkedActivationID(t, env, "task")
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: parkedID,
			Action:       AdminActionComplete,
			Reason:       "supplying the missing value directly",
		})
	}, time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{
		"global_user_email": "user@example.com",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	require.Equal(t, ParkCategoryInputMapping, captured.ParkCategory)
	require.Equal(t, map[string]string{"missing_global_var": "local_key"}, captured.InputMapping)
	require.Empty(t, captured.OutputMapping)
	require.Equal(t, parkedID, captured.ActivationID, "AdminParkPayload.ActivationID matches NodeInfo's")
	env.AssertExpectations(t)
}

// TestAdminParkNotificationFailureRecordedBeforeWorkflowCompletes pins the join in
// parkNodeForAdmin that waits for notifyAdminPark's background coroutine before acting on a
// resolution. AdminParkActivity here sleeps 300ms (real time, not workflow virtual time) before
// failing, while the resolution signal arrives after just 1ms — so the resolution reliably wins
// the race and the workflow tries to complete while the notification coroutine is still
// in-flight. Temporal abandons (does not drain) a workflow.Go coroutine that hasn't finished when
// the workflow function returns, so without the join this test reproduces the bug deterministically:
// the workflow completes in milliseconds without ever recording the notification's eventual
// failure on AuditTrail.
func TestAdminParkNotificationFailureRecordedBeforeWorkflowCompletes(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("AdminParkActivity", mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ AdminParkPayload) error {
			time.Sleep(300 * time.Millisecond)
			return errors.New("notification sink unavailable")
		}).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: parkedActivationID(t, env, "task"),
			Action:       AdminActionComplete,
			Reason:       "racing the slow notification",
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

	trail := strings.Join(instance.AuditTrail, "\n")
	require.Contains(t, trail, "admin park notification failed")
	require.Contains(t, trail, "notification sink unavailable")
	require.Less(t, strings.Index(trail, "admin park notification failed"), strings.Index(trail, "admin resolution: COMPLETE"),
		"the notification's failure must be recorded before the resolution it raced against")

	env.AssertExpectations(t)
}

// TestAdminRetryResolvesInputMappingError parks on a missing input mapping, then resolves it
// with AdminActionRetry supplying the missing variable in the variables patch. Since the Activity
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
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_WITH_MISSING_INPUT", mock.Anything, mock.Anything, mock.Anything).
		Return(map[string]any{}, nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:                 "task",
			ActivationID:           parkedActivationID(t, env, "task"),
			Action:                 AdminActionRetry,
			WorkflowVariablesPatch: map[string]any{"missing_global_var": "fixed-value"},
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

// TestAdminCompleteResolvesOutputMappingErrorWithoutReinvokingActivity parks on a missing
// output mapping key — meaning the Activity already ran successfully. Resolving with
// AdminActionComplete must supply the value directly without re-invoking the Activity again.
func TestAdminCompleteResolvesOutputMappingErrorWithoutReinvokingActivity(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingRequiredOutputWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_MISSING_REQUIRED_OUTPUT", mock.Anything, mock.Anything, mock.Anything).
		Return(map[string]any{}, nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	// Confirm the parked LastError explicitly warns that the Activity already ran, before
	// resolving it. Because this node parks on an *output* mapping error, ExecuteTaskActivity
	// must finish running before the park sets LastError; 100ms gives the Activity time to
	// actually complete in the test environment before we query — same timing characteristic
	// seen elsewhere in this suite. (A 1ms query races the Activity and reads an empty
	// LastError.)
	var parkedID string
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		require.Contains(t, instance.NodeInfo["task"].LastError, "already completed successfully")
		require.Contains(t, instance.NodeInfo["task"].LastError, "use COMPLETE instead of RETRY")
		parkedID = instance.NodeInfo["task"].ActivationID
	}, 100*time.Millisecond)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:                 "task",
			ActivationID:           parkedID,
			Action:                 AdminActionComplete,
			WorkflowVariablesPatch: map[string]any{"global_user_phone": "555-1234"},
		})
	}, 200*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Equal(t, StatusCompleted, instance.Status)
	require.Equal(t, "555-1234", instance.WorkflowVariables["global_user_phone"])
	require.Empty(t, instance.NodeInfo["task"].CachedTaskResult)

	// .Once() above already enforces this, but AssertExpectations makes the intent explicit:
	// the Activity that already ran must not be invoked a second time by the complete.
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
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_MISSING_REQUIRED_OUTPUT", mock.Anything, mock.Anything, mock.Anything).
		Return(map[string]any{"attempt": "first"}, nil).Once()
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_MISSING_REQUIRED_OUTPUT", mock.Anything, mock.Anything, mock.Anything).
		Return(map[string]any{"attempt": "second"}, nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	// Retry without fixing anything — the Activity runs again, output mapping fails again.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: parkedActivationID(t, env, "task"),
			Action:       AdminActionRetry,
		})
	}, time.Millisecond)

	// Confirm the re-park reflects this latest attempt, not a stale leftover from the first.
	// (100ms gives the second Activity invocation time to actually complete in the test
	// environment before we query — same timing characteristic seen elsewhere in this suite.)
	var secondParkID string
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		require.Equal(t, NodeStatusAwaitingAdmin, instance.NodeInfo["task"].Status)
		require.Equal(t, "second", instance.NodeInfo["task"].CachedTaskResult["attempt"])
		require.Contains(t, instance.NodeInfo["task"].LastError, "already completed successfully")
		secondParkID = instance.NodeInfo["task"].ActivationID
	}, 100*time.Millisecond)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:                 "task",
			ActivationID:           secondParkID,
			Action:                 AdminActionComplete,
			WorkflowVariablesPatch: map[string]any{"global_user_phone": "555-1234"},
		})
	}, 200*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestAdminCompleteWithEmptyPatchContinuesPastParkedNode resolves a parked node with
// AdminActionComplete and no patch: no variables are set, but the graph still continues past it to the END node.
func TestAdminCompleteWithEmptyPatchContinuesPastParkedNode(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: parkedActivationID(t, env, "task"),
			Action:       AdminActionComplete,
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

	// END node's WorkflowCompletedActivity firing proves execution continued past the node.
	env.AssertExpectations(t)
}

// TestAdminResolutionUnknownActivationIDAndMalformedActionAreNoOps verifies that a resolution
// signal targeting an ActivationID nobody is waiting on, and a signal with a garbage Action, are
// both silently ignored rather than failing the node — only a deliberate, well-formed
// resolution should ever move a parked node forward.
func TestAdminResolutionUnknownActivationIDAndMalformedActionAreNoOps(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "no-such-node",
			ActivationID: "no-such-activation-id",
			Action:       AdminActionAbort,
		})
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: parkedActivationID(t, env, "task"),
			Action:       "NOT_A_REAL_ACTION",
		})
	}, 2*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: parkedActivationID(t, env, "task"),
			Action:       AdminActionAbort,
		})
	}, 3*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{
		"global_user_email": "user@example.com",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Contains(t, env.GetWorkflowError().Error(), "input mapping error")
}

// TestStaleAdminResolutionSignalCannotResolveALaterParkOfTheSameNode is the regression test for
// the bug ActivationID-based routing fixes: when resolution signals were matched by plain NodeID,
// a signal meant for an earlier parking of a node could incorrectly resolve a later, unrelated
// parking of that same node (e.g. after a Retry that fails again). A Retry mints a new
// ActivationID before re-dispatching, so the node's second parking has a different ActivationID
// than its first — proving a signal echoing the stale, first one is dropped rather than matched to
// the second.
func TestStaleAdminResolutionSignalCannotResolveALaterParkOfTheSameNode(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	var firstParkID, secondParkID string

	// Retry without fixing anything: the input mapping error recurs, so the node re-parks under a
	// new ActivationID.
	env.RegisterDelayedCallback(func() {
		firstParkID = parkedActivationID(t, env, "task")
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: firstParkID,
			Action:       AdminActionRetry,
		})
	}, time.Millisecond)

	// A stale signal echoing the FIRST park's ActivationID, arriving after the node has already
	// re-parked under a second one, must be dropped rather than resolve the second parking.
	env.RegisterDelayedCallback(func() {
		secondParkID = parkedActivationID(t, env, "task")
		require.NotEqual(t, firstParkID, secondParkID, "the retry's re-park must mint a new ActivationID")
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: firstParkID,
			Action:       AdminActionAbort,
		})
	}, 2*time.Millisecond)

	// Confirm the stale signal changed nothing: the node is still parked, not aborted.
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		require.Equal(t, NodeStatusAwaitingAdmin, instance.NodeInfo["task"].Status,
			"a stale signal for an earlier parking must not resolve the current one")
	}, 3*time.Millisecond)

	// The current ActivationID does resolve it.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: secondParkID,
			Action:       AdminActionComplete,
			Reason:       "supplying the missing value directly",
		})
	}, 4*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{
		"global_user_email": "user@example.com",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Equal(t, StatusCompleted, instance.Status)
	require.Equal(t, NodeStatusCompleted, instance.NodeInfo["task"].Status)
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

// TestAdminCompleteRejectedForParkedGatewayNode proves that a parked GATEWAY node
// cannot be resolved with Complete, with or without a patch (it would bypass the gateway's real routing
// logic by blindly taking its first outgoing edge) — only Retry (after correcting the
// missing variable) or Abort are accepted.
func TestAdminCompleteRejectedForParkedGatewayNode(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(gatewayParkWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_PASS", mock.Anything, mock.Anything, mock.Anything).
		Return(map[string]any{}, nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	// Rejecting a GATEWAY's Complete just continues parkNodeForAdmin's loop without re-minting, so
	// the node re-parks under the same ActivationID each time — one query is enough for every signal.
	var gatewayParkID string

	// Complete without a patch: rejected, node stays parked.
	env.RegisterDelayedCallback(func() {
		gatewayParkID = parkedActivationID(t, env, "gateway")
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "gateway",
			ActivationID: gatewayParkID,
			Action:       AdminActionComplete,
		})
	}, time.Millisecond)

	// Complete with a patch: rejected, node stays parked.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:                 "gateway",
			ActivationID:           gatewayParkID,
			Action:                 AdminActionComplete,
			WorkflowVariablesPatch: map[string]any{"decision": "pass"},
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
			NodeID:                 "gateway",
			ActivationID:           gatewayParkID,
			Action:                 AdminActionRetry,
			WorkflowVariablesPatch: map[string]any{"decision": "pass"},
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
// TestAdminParkingIsolatesParallelBranches exercises PARALLEL_SPLIT's isolation: each
// matching branch runs as its own child workflow (see parallel_gateway.go), so a node parked
// for admin inside one branch (task_a, here) shows up in that child's own status and signal
// channel, not the parent's (similar to BATCH_SPLIT partitions).
// The child's deterministic ID is FormatChildWorkflowID(rootID, parentID, splitNodeID, edgeID);
// parallelWorkflowJSON's split->task_a edge is "e2".
func TestAdminParkingIsolatesParallelBranches(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(parallelWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_A", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("boom", "TaskFailure", nil)).Once()
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_B", mock.Anything, mock.Anything, mock.Anything).
		Return(map[string]any{}, nil).Once()

	branchWorkflowID := FormatChildWorkflowID("default-test-workflow-id", "default-test-workflow-id", "split", "e2")

	var parkedID string
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflowByID(branchWorkflowID, "GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))

		require.Equal(t, NodeStatusAwaitingAdmin, instance.NodeInfo["task_a"].Status)
		parkedID = instance.NodeInfo["task_a"].ActivationID
	}, time.Second)

	env.RegisterDelayedCallback(func() {
		require.NoError(t, env.SignalWorkflowByID(branchWorkflowID, AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task_a",
			ActivationID: parkedID,
			Action:       AdminActionAbort,
		}))
	}, 2*time.Second)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Contains(t, env.GetWorkflowError().Error(), "boom")

	env.AssertExpectations(t)
	env.AssertNotCalled(t, "ExecuteTaskActivity", mock.Anything, "TASK_C", mock.Anything, mock.Anything, mock.Anything)
}

// TestAdminCompleteResolvesWaitForSignalOutputMappingError verifies that when a
// SIGNALING WAIT node parks due to an output mapping error, the received signal payload
// is cached in NodeInfo.CachedTaskResult and can be successfully completed by the admin with a variables patch.
func TestAdminCompleteResolvesWaitForSignalOutputMappingError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	waitSignalJSON := `
	{
		"workflow_id": "wait-signal-park-test",
		"name": "Wait Signal Park Test",
		"version": 1,
		"edges":[
			{ "id": "e1", "source_id": "start", "target_id": "wait" },
			{ "id": "e2", "source_id": "wait", "target_id": "end" }
		],
		"nodes":[
			{ "id": "start", "type": "START" },
			{
				"id": "wait",
				"type": "SIGNALING",
				"signaling": {
					"type": "WAIT",
					"signal_name": "my_test_signal"
				},
				"output_mapping": {
					"missing_key": "global_target"
				}
			},
			{ "id": "end", "type": "END" }
		]
	}`

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(waitSignalJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	// 1. Send the signal, but omit the "missing_key" key to trigger output mapping failure
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("my_test_signal", map[string]any{
			"incorrect_key": "value",
		})
	}, time.Millisecond)

	// 2. Query to verify status is parked and CachedTaskResult holds the signal payload
	var parkedID string
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		require.Equal(t, NodeStatusAwaitingAdmin, instance.NodeInfo["wait"].Status)
		require.Contains(t, instance.NodeInfo["wait"].LastError, "output mapping error")
		require.Equal(t, "value", instance.NodeInfo["wait"].CachedTaskResult["incorrect_key"])
		parkedID = instance.NodeInfo["wait"].ActivationID
	}, 2*time.Millisecond)

	// 3. Resolve the parked node with AdminActionComplete
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:                 "wait",
			ActivationID:           parkedID,
			Action:                 AdminActionComplete,
			WorkflowVariablesPatch: map[string]any{"global_target": "resolved-value"},
		})
	}, 3*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{
		"signal_name": "my_test_signal",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Equal(t, StatusCompleted, instance.Status)
	require.Equal(t, "resolved-value", instance.WorkflowVariables["global_target"])
	require.Empty(t, instance.NodeInfo["wait"].CachedTaskResult)
}

// TestWaitForSignalCancellationCleanlyPropagates verifies that when a workflow is canceled while
// blocked on a SIGNALING WAIT node, the node propagates the cancellation error immediately
// and the workflow fails with a canceled error instead of parking the node for admin intervention.
func TestWaitForSignalCancellationCleanlyPropagates(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	waitSignalJSON := `
	{
		"workflow_id": "wait-signal-cancel-test",
		"name": "Wait Signal Cancel Test",
		"version": 1,
		"edges":[
			{ "id": "e1", "source_id": "start", "target_id": "wait" },
			{ "id": "e2", "source_id": "wait", "target_id": "end" }
		],
		"nodes":[
			{ "id": "start", "type": "START" },
			{
				"id": "wait",
				"type": "SIGNALING",
				"signaling": {
					"type": "WAIT",
					"signal_name": "my_test_signal"
				}
			},
			{ "id": "end", "type": "END" }
		]
	}`

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(waitSignalJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})

	// Cancel the workflow shortly after it starts blocking on the signal
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{
		"signal_name": "my_test_signal",
	})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	// Verify that it is a canceled error, which is not an admin parking/abort error
	require.Contains(t, err.Error(), "canceled")

	// Verify that the node is NOT awaiting admin
	val, queryErr := env.QueryWorkflow("GetStatus")
	require.NoError(t, queryErr)
	var instance WorkflowInstance
	require.NoError(t, val.Get(&instance))
	require.NotEqual(t, NodeStatusAwaitingAdmin, instance.NodeInfo["wait"].Status)
}
