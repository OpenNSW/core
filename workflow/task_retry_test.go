// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

const singleTaskWorkflowJSON = `
{
  "workflow_id": "single-task",
  "name": "Single Task",
  "version": 1,
  "edges":[
    { "id": "e1", "source_id": "start", "target_id": "task" },
    { "id": "e2", "source_id": "task", "target_id": "end" }
  ],
  "nodes":[
    { "id": "start", "type": "START" },
    { "id": "task", "type": "TASK", "task_template_id": "FLAKY" },
    { "id": "end", "type": "END" }
  ]
}`

// newSingleTaskEnv returns a test environment running singleTaskWorkflowJSON, whose one task's Activity
// answers with execute(attempt), attempt counting from 1.
func newSingleTaskEnv(t *testing.T, execute func(attempt int) (map[string]any, error)) (*testsuite.TestWorkflowEnvironment, WorkflowDefinition, *int) {
	t.Helper()
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(singleTaskWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})

	attempts := 0
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "FLAKY", mock.Anything, mock.Anything, mock.Anything).
		Return(func(context.Context, string, map[string]any, string, ActivationRef) (map[string]any, error) {
			attempts++
			return execute(attempts)
		})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	return env, def, &attempts
}

// A task whose error clears is retried until it succeeds.
func TestTaskRetriesTransientErrorUntilItSucceeds(t *testing.T) {
	env, def, attempts := newSingleTaskEnv(t, func(attempt int) (map[string]any, error) {
		if attempt < 3 {
			return nil, errors.New("agency unavailable")
		}
		return map[string]any{}, nil
	})

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, 3, *attempts)
}

// A task whose error never clears stops retrying after taskMaxAttempts and parks for an admin,
// instead of retrying forever.
func TestTaskParksWhenRetriesRunOut(t *testing.T) {
	env, def, attempts := newSingleTaskEnv(t, func(int) (map[string]any, error) {
		return nil, errors.New("agency unavailable")
	})

	// The retries take about 24h of (skipped) workflow time; check well after they run out.
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		node := instance.NodeInfo["task"]
		require.Equal(t, NodeStatusAwaitingAdmin, node.Status)
		require.Equal(t, ParkCategoryTaskFailure, node.ParkCategory)
		require.Contains(t, node.LastError, "agency unavailable")

		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "task",
			ActivationID: node.ActivationID,
			Action:       AdminActionAbort,
		})
	}, 48*time.Hour)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, taskMaxAttempts, *attempts)
}

// A completion hook whose error never clears stops retrying after taskMaxAttempts and parks the END
// node, instead of retrying forever.
func TestCompletionHookParksWhenRetriesRunOut(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(startEndWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	attempts := 0
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(context.Context, string, map[string]any, int64) error {
			attempts++
			return errors.New("database unavailable")
		})

	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		node := instance.NodeInfo["end"]
		require.Equal(t, NodeStatusAwaitingAdmin, node.Status)
		require.Equal(t, ParkCategoryTaskFailure, node.ParkCategory)

		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID:       "end",
			ActivationID: node.ActivationID,
			Action:       AdminActionAbort,
		})
	}, 48*time.Hour)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, taskMaxAttempts, attempts)
}
