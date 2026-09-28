// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// stepSeen is what ExecuteTaskActivity observed for one run of a node.
type stepSeen struct {
	activityID string
	template   string
	step       ActivationRef
}

// runRecordingSteps runs def with every ExecuteTaskActivity answered by result(template, callNo),
// and returns what each activity saw, in call order, and the finished instance.
func runRecordingSteps(t *testing.T, defJSON string, result func(template string, call int) map[string]any) ([]stepSeen, WorkflowInstance) {
	t.Helper()
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(defJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})

	var (
		mu   sync.Mutex
		seen []stepSeen
	)
	env.OnActivity("ExecuteTaskActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, template string, _ map[string]any, _ string, step ActivationRef) (map[string]any, error) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, stepSeen{activityID: activity.GetInfo(ctx).ActivityID, template: template, step: step})
			return result(template, len(seen)), nil
		})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	return seen, instance
}

// A node revisited by a loop is a new step each time: new step ID, higher seq, same node ID.
func TestTaskNodeInLoopGetsNewActivationIDEachRun(t *testing.T) {
	// The poll answers "not delivered" twice, then "delivered", so the node runs three times.
	seen, instance := runRecordingSteps(t, timerPollWorkflowJSON, func(_ string, call int) map[string]any {
		return map[string]any{"delivered": call >= 3}
	})

	require.Len(t, seen, 3)
	ids := map[string]bool{}
	for i, s := range seen {
		require.Equal(t, "poll", s.step.NodeID, "the node ID names the definition and repeats")
		require.Equal(t, int64(i+1), s.step.Seq, "seq grows by one per step")
		require.Equal(t, newActivationID(instance.ID, "poll", s.step.Seq), s.activityID, "the step ID is the Activity ID")
		_, err := uuid.Parse(s.activityID)
		require.NoError(t, err, "a step ID is a UUID")
		ids[s.activityID] = true
	}
	require.Len(t, ids, 3, "every run has its own step ID")

	info := instance.NodeInfo["poll"]
	require.Equal(t, "poll", info.ID)
	require.Equal(t, int64(3), info.Seq)
	require.Equal(t, seen[2].activityID, info.ActivationID, "NodeInfo describes the latest run")
}

// The counter is workflow-wide, not per node.
func TestSeqIsWorkflowWideAcrossNodes(t *testing.T) {
	seen, instance := runRecordingSteps(t, nodeOutputToSubsetInputWorkflowJSON, func(template string, _ int) map[string]any {
		if template == "NODE1_TASK" {
			return map[string]any{"task_email": "a@b.c", "task_phone": "1"}
		}
		return map[string]any{}
	})

	require.Len(t, seen, 2)
	require.Equal(t, ActivationRef{NodeID: "node1", Seq: 1}, seen[0].step)
	require.Equal(t, ActivationRef{NodeID: "node2", Seq: 2}, seen[1].step)
	require.NotEqual(t, seen[0].activityID, seen[1].activityID)
	require.Equal(t, int64(1), instance.NodeInfo["node1"].Seq)
	require.Equal(t, int64(2), instance.NodeInfo["node2"].Seq)
}

// Step IDs are derived, not random, so they come out the same on every replay of the workflow.
func TestNewActivationIDIsDeterministicAndDistinct(t *testing.T) {
	base := newActivationID("wf-1", "node", 1)
	require.Equal(t, base, newActivationID("wf-1", "node", 1))
	require.NotEqual(t, base, newActivationID("wf-1", "node", 2), "seq")
	require.NotEqual(t, base, newActivationID("wf-1", "other", 1), "node")
	require.NotEqual(t, base, newActivationID("wf-2", "node", 1), "workflow")
	_, err := uuid.Parse(base)
	require.NoError(t, err)
}
