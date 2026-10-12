// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// These run against a real Temporal server, like complete_activation_integration_test.go: set
// TEMPORAL_TEST_ADDRESS to run them.

// A force-parked step parks its node instead of being retried, and the node is then resolved like
// any parked node: RETRY runs it again under a new step.
func TestForceParkActivationParksTheNode(t *testing.T) {
	mgr, workflowID, runs := startPendingLoop(t)
	ctx := context.Background()
	parker := mgr.(ActivationParker)

	first := nextRun(t, runs)
	require.NoError(t, parker.ForceParkActivation(ctx, workflowID, "", first.ActivationID, "warranted offline"))

	var parked *NodeInfo
	require.Eventually(t, func() bool {
		st, err := mgr.GetStatus(ctx, workflowID)
		if err != nil {
			return false
		}
		parked = st.NodeInfo[first.NodeID]
		return parked != nil && parked.Status == NodeStatusAwaitingAdmin
	}, 20*time.Second, 200*time.Millisecond)
	require.Equal(t, ParkCategoryTaskFailure, parked.ParkCategory)
	require.Contains(t, parked.LastError, ForcedParkErrorType)
	require.Contains(t, parked.LastError, "warranted offline")
	require.Equal(t, first.ActivationID, parked.ActivationID, "parking keeps the step it failed")

	select {
	case p := <-runs:
		t.Fatalf("the step was retried instead of parked: new run %s", p.ActivationID)
	case <-time.After(time.Second):
	}

	resolver := mgr.(AdminInterventionResolver)
	require.NoError(t, resolver.ResolveAdminIntervention(ctx, workflowID, "", AdminResolutionSignal{
		ActivationID: parked.ActivationID,
		Action:       AdminActionRetry,
		Reason:       "run it again",
	}))
	second := nextRun(t, runs)
	require.Equal(t, first.NodeID, second.NodeID)
	require.NotEqual(t, first.ActivationID, second.ActivationID)
}

func TestForceParkActivationOfAStepThatIsNotPending(t *testing.T) {
	mgr, workflowID, runs := startPendingLoop(t)
	ctx := context.Background()
	parker := mgr.(ActivationParker)
	first := nextRun(t, runs)

	err := parker.ForceParkActivation(ctx, workflowID, "", uuid.NewString(), "reason")
	require.ErrorIs(t, err, ErrActivationNotPending)

	// A step that has completed is no longer pending either.
	require.NoError(t, mgr.CompleteActivation(ctx, workflowID, "", first.ActivationID, map[string]any{"delivered": true}))
	err = parker.ForceParkActivation(ctx, workflowID, "", first.ActivationID, "reason")
	require.ErrorIs(t, err, ErrActivationNotPending)
}

func TestTerminateWorkflow(t *testing.T) {
	mgr, workflowID, runs := startPendingLoop(t)
	ctx := context.Background()
	terminator := mgr.(WorkflowTerminator)
	first := nextRun(t, runs)

	require.NoError(t, terminator.TerminateWorkflow(ctx, workflowID, "", "task cancelled"))

	// A terminated workflow runs nothing: its pending step can't be completed.
	err := mgr.CompleteActivation(ctx, workflowID, "", first.ActivationID, map[string]any{"delivered": true})
	require.ErrorIs(t, err, ErrActivationNotPending)

	// A closed workflow has nothing left to stop.
	err = terminator.TerminateWorkflow(ctx, workflowID, "", "again")
	require.ErrorIs(t, err, ErrWorkflowNotFound)
}
