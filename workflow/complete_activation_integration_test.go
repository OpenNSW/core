// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
)

// These tests run against a real Temporal server, because they check what the server itself
// answers to a completion for a step that is not pending. Set TEMPORAL_TEST_ADDRESS to run them,
// for example against `temporal server start-dev`; they are skipped otherwise.

// loopUntilDeliveredJSON polls in a loop with no timer: poll -> gw -> (delivered ? end : poll).
const loopUntilDeliveredJSON = `
{
  "workflow_id": "loop-until-delivered",
  "name": "Loop until delivered",
  "version": 1,
  "edges":[
    { "id": "e1", "source_id": "start", "target_id": "poll" },
    { "id": "e2", "source_id": "poll", "target_id": "gw" },
    { "id": "e3", "source_id": "gw", "target_id": "end", "condition": "delivered == true" },
    { "id": "e4", "source_id": "gw", "target_id": "poll", "condition": "delivered != true" }
  ],
  "nodes":[
    { "id": "start", "type": "START" },
    { "id": "poll", "type": "TASK", "task_template_id": "POLL", "output_mapping": { "delivered": "delivered" } },
    { "id": "gw", "type": "GATEWAY", "gateway_type": "EXCLUSIVE_SPLIT" },
    { "id": "end", "type": "END" }
  ]
}`

// startPendingLoop runs loopUntilDeliveredJSON on a real server with a handler that parks every TASK
// run. It returns the manager, the workflow ID and a channel that yields each run's payload.
func startPendingLoop(t *testing.T) (TemporalManager, string, <-chan TaskPayload) {
	t.Helper()
	addr := os.Getenv("TEMPORAL_TEST_ADDRESS")
	if addr == "" {
		t.Skip("TEMPORAL_TEST_ADDRESS is not set")
	}
	c, err := client.Dial(client.Options{HostPort: addr})
	require.NoError(t, err)
	t.Cleanup(c.Close)

	runs := make(chan TaskPayload, 8)
	var mu sync.Mutex
	mgr := NewTemporalManager(c, "default", "task-done-it-"+uuid.NewString(),
		func(p TaskPayload) (map[string]any, error) {
			mu.Lock()
			defer mu.Unlock()
			runs <- p
			return nil, activity.ErrResultPending
		},
		func(WorkflowCompletion) error { return nil })
	require.NoError(t, mgr.StartWorker())
	t.Cleanup(mgr.StopWorker)

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(loopUntilDeliveredJSON), &def))
	workflowID := "loop-" + uuid.NewString()
	require.NoError(t, mgr.StartWorkflow(context.Background(), workflowID, def, map[string]any{}))
	return mgr, workflowID, runs
}

func nextRun(t *testing.T, runs <-chan TaskPayload) TaskPayload {
	t.Helper()
	select {
	case p := <-runs:
		return p
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the workflow to start a TASK run")
		return TaskPayload{}
	}
}

// The bug this design exists to fix: a late completion for an earlier pass of a looping node used to
// hit the current pass, because both had the same Activity ID. Each pass is now its own step.
func TestCompleteStepRejectsAnEarlierRunOfALoopingNode(t *testing.T) {
	mgr, workflowID, runs := startPendingLoop(t)
	ctx := context.Background()

	first := nextRun(t, runs)
	require.Equal(t, int64(1), first.Seq)
	require.NoError(t, mgr.CompleteActivation(ctx, workflowID, "", first.ActivationID, map[string]any{"delivered": false}))

	second := nextRun(t, runs) // the loop came back to the same node
	require.Equal(t, first.NodeID, second.NodeID, "same node in the definition")
	require.NotEqual(t, first.ActivationID, second.ActivationID, "but a new step")
	require.Equal(t, int64(2), second.Seq)

	// A late or duplicate completion for the first pass is rejected and must not touch the second.
	err := mgr.CompleteActivation(ctx, workflowID, "", first.ActivationID, map[string]any{"delivered": true})
	require.ErrorIs(t, err, ErrActivationNotPending)

	// The second pass is still waiting and completes normally.
	require.NoError(t, mgr.CompleteActivation(ctx, workflowID, "", second.ActivationID, map[string]any{"delivered": true}))
	require.Eventually(t, func() bool {
		st, err := mgr.GetStatus(ctx, workflowID)
		return err == nil && st.Status == StatusCompleted
	}, 20*time.Second, 200*time.Millisecond)
}

func TestCompleteStepOfAStepThatNeverExistedIsNotPending(t *testing.T) {
	mgr, workflowID, runs := startPendingLoop(t)
	first := nextRun(t, runs)

	err := mgr.CompleteActivation(context.Background(), workflowID, "", uuid.NewString(), map[string]any{"delivered": true})
	require.ErrorIs(t, err, ErrActivationNotPending)

	// The real step is untouched.
	require.NoError(t, mgr.CompleteActivation(context.Background(), workflowID, "", first.ActivationID, map[string]any{"delivered": true}))
}

func TestCompleteStepAfterTheWorkflowEndedIsNotPending(t *testing.T) {
	mgr, workflowID, runs := startPendingLoop(t)
	first := nextRun(t, runs)
	require.NoError(t, mgr.CompleteActivation(context.Background(), workflowID, "", first.ActivationID, map[string]any{"delivered": true}))
	require.Eventually(t, func() bool {
		st, err := mgr.GetStatus(context.Background(), workflowID)
		return err == nil && st.Status == StatusCompleted
	}, 20*time.Second, 200*time.Millisecond)

	err := mgr.CompleteActivation(context.Background(), workflowID, "", first.ActivationID, map[string]any{"delivered": true})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrActivationNotPending), "got %v", err)
}
