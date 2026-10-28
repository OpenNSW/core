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
	"go.temporal.io/sdk/temporal"
)

// CompleteActivation sends the step's output as the workflow's CompleteActivation Update, with no
// Update ID of its own, and waits for the workflow to accept it.
func TestTemporalManagerImpl_CompleteActivation_SendsTheUpdate(t *testing.T) {
	mockClient := &mocks.Client{}
	handle := &mocks.WorkflowUpdateHandle{}
	output := map[string]any{"k": "v"}
	mockClient.On("UpdateWorkflow", mock.Anything, client.UpdateWorkflowOptions{
		WorkflowID:   "wf-1",
		RunID:        "run-1",
		UpdateName:   completeActivationUpdate,
		Args:         []any{"step-1", output},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	}).Return(handle, nil)
	handle.On("Get", mock.Anything, nil).Return(nil)

	m := &temporalManagerImpl{temporalClient: mockClient}
	require.NoError(t, m.CompleteActivation(context.Background(), "wf-1", "run-1", "step-1", output))
	mockClient.AssertExpectations(t)
	handle.AssertExpectations(t)
}

func TestTemporalManagerImpl_CompleteActivation_NotPending(t *testing.T) {
	rejected := temporal.NewApplicationError("step step-1 is not waiting to be completed", activationNotPendingErrorType)

	for name, tc := range map[string]struct {
		updateErr, getErr error
	}{
		"the workflow rejects it":           {getErr: rejected},
		"the workflow has ended or is gone": {updateErr: serviceerror.NewNotFound("workflow execution already completed")},
	} {
		t.Run(name, func(t *testing.T) {
			mockClient := &mocks.Client{}
			handle := &mocks.WorkflowUpdateHandle{}
			if tc.updateErr != nil {
				mockClient.On("UpdateWorkflow", mock.Anything, mock.Anything).Return(nil, tc.updateErr)
			} else {
				mockClient.On("UpdateWorkflow", mock.Anything, mock.Anything).Return(handle, nil)
				handle.On("Get", mock.Anything, nil).Return(tc.getErr)
			}

			m := &temporalManagerImpl{temporalClient: mockClient}
			err := m.CompleteActivation(context.Background(), "wf-1", "", "step-1", nil)
			require.ErrorIs(t, err, ErrActivationNotPending)
		})
	}
}

// Any other failure is returned as it is, not as ErrActivationNotPending: the caller may retry it.
func TestTemporalManagerImpl_CompleteActivation_OtherErrorPassesThrough(t *testing.T) {
	mockClient := &mocks.Client{}
	underlying := serviceerror.NewUnavailable("temporal unavailable")
	mockClient.On("UpdateWorkflow", mock.Anything, mock.Anything).Return(nil, underlying)

	m := &temporalManagerImpl{temporalClient: mockClient}
	err := m.CompleteActivation(context.Background(), "wf-1", "", "step-1", nil)
	require.ErrorIs(t, err, underlying)
	require.False(t, errors.Is(err, ErrActivationNotPending))
}

func TestTemporalManagerImpl_ForceParkActivation_SendsTheUpdate(t *testing.T) {
	mockClient := &mocks.Client{}
	handle := &mocks.WorkflowUpdateHandle{}
	mockClient.On("UpdateWorkflow", mock.Anything, client.UpdateWorkflowOptions{
		WorkflowID:   "wf-1",
		RunID:        "run-1",
		UpdateName:   forceParkActivationUpdate,
		Args:         []any{"step-1", "warranted offline"},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	}).Return(handle, nil)
	handle.On("Get", mock.Anything, nil).Return(nil)

	m := &temporalManagerImpl{temporalClient: mockClient}
	require.NoError(t, m.ForceParkActivation(context.Background(), "wf-1", "run-1", "step-1", "warranted offline"))
	mockClient.AssertExpectations(t)
	handle.AssertExpectations(t)
}

func TestTemporalManagerImpl_ForceParkActivation_NotPending(t *testing.T) {
	rejected := temporal.NewApplicationError("step step-1 is not waiting to be completed", activationNotPendingErrorType)

	for name, tc := range map[string]struct {
		updateErr, getErr error
	}{
		"the workflow rejects it":           {getErr: rejected},
		"the workflow has ended or is gone": {updateErr: serviceerror.NewNotFound("workflow execution already completed")},
	} {
		t.Run(name, func(t *testing.T) {
			mockClient := &mocks.Client{}
			handle := &mocks.WorkflowUpdateHandle{}
			if tc.updateErr != nil {
				mockClient.On("UpdateWorkflow", mock.Anything, mock.Anything).Return(nil, tc.updateErr)
			} else {
				mockClient.On("UpdateWorkflow", mock.Anything, mock.Anything).Return(handle, nil)
				handle.On("Get", mock.Anything, nil).Return(tc.getErr)
			}

			m := &temporalManagerImpl{temporalClient: mockClient}
			err := m.ForceParkActivation(context.Background(), "wf-1", "", "step-1", "reason")
			require.ErrorIs(t, err, ErrActivationNotPending)
		})
	}
}

func TestTemporalManagerImpl_TerminateWorkflow_NotFound(t *testing.T) {
	mockClient := &mocks.Client{}
	mockClient.On("TerminateWorkflow", mock.Anything, "wf-1", "", "cancelled", mock.Anything).
		Return(serviceerror.NewNotFound("workflow execution already completed"))

	m := &temporalManagerImpl{temporalClient: mockClient}
	err := m.TerminateWorkflow(context.Background(), "wf-1", "", "cancelled")
	require.ErrorIs(t, err, ErrWorkflowNotFound)
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
