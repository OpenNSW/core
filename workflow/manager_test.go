// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

// TestTemporalManagerImpl_TaskDone_UsesConfiguredNamespace guards against TaskDone completing the
// activity in a namespace other than the one the manager was configured with.
func TestTemporalManagerImpl_TaskDone_UsesConfiguredNamespace(t *testing.T) {
	mockClient := &mocks.Client{}
	output := map[string]any{"k": "v"}
	mockClient.On("CompleteActivityByID", mock.Anything, "staging", "wf-1", "run-1", "node-1", output, nil).
		Return(nil)

	// Go through the constructor so the test also covers it storing the namespace. worker.New
	// rejects mocks, so construct with a lazy (non-connecting) client, then swap in the mock.
	lazyClient, err := client.NewLazyClient(client.Options{Namespace: "staging"})
	require.NoError(t, err)
	m := NewTemporalManager(lazyClient, "staging", "some-queue", nil, nil).(*temporalManagerImpl)
	m.temporalClient = mockClient

	require.NoError(t, m.TaskDone(context.Background(), "wf-1", "run-1", "node-1", output))
	mockClient.AssertExpectations(t)
}

func TestTemporalManagerImpl_GetStatus_NotFound(t *testing.T) {
	mockClient := &mocks.Client{}
	mockClient.On("QueryWorkflow", mock.Anything, "missing-workflow", "", "GetStatus").
		Return(nil, serviceerror.NewNotFound("workflow not found"))

	m := &temporalManagerImpl{temporalClient: mockClient}

	instance, err := m.GetStatus(context.Background(), "missing-workflow")

	require.Nil(t, instance)
	require.True(t, errors.Is(err, ErrWorkflowNotFound))
	// The original Temporal error's message must survive the wrap, so logs aren't
	// left with just the generic sentinel text.
	require.Contains(t, err.Error(), "workflow not found")
}

// TestTemporalManagerImpl_GetStatus_OtherErrorPassesThrough guards against a future change
// widening the NotFound type check and accidentally reclassifying an unrelated Temporal error
// (e.g. a permission or internal error) as ErrWorkflowNotFound.
func TestTemporalManagerImpl_GetStatus_OtherErrorPassesThrough(t *testing.T) {
	mockClient := &mocks.Client{}
	underlying := serviceerror.NewInternal("temporal internal error")
	mockClient.On("QueryWorkflow", mock.Anything, "some-workflow", "", "GetStatus").
		Return(nil, underlying)

	m := &temporalManagerImpl{temporalClient: mockClient}

	instance, err := m.GetStatus(context.Background(), "some-workflow")

	require.Nil(t, instance)
	require.False(t, errors.Is(err, ErrWorkflowNotFound))
	require.Equal(t, underlying, err)
}
