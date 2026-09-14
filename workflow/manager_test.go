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
	"go.temporal.io/sdk/mocks"
)

func TestTemporalManagerImpl_GetStatus_NotFound(t *testing.T) {
	mockClient := &mocks.Client{}
	mockClient.On("QueryWorkflow", mock.Anything, "missing-workflow", "", "GetStatus").
		Return(nil, serviceerror.NewNotFound("workflow not found"))

	m := &temporalManagerImpl{temporalClient: mockClient}

	instance, err := m.GetStatus(context.Background(), "missing-workflow")

	require.Nil(t, instance)
	require.True(t, errors.Is(err, ErrWorkflowNotFound))
}
