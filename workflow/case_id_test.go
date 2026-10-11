// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

const startEndWorkflowJSON = `{
	"id": "start_end",
	"nodes": [{"id": "start", "type": "START"}, {"id": "end", "type": "END"}],
	"edges": [{"id": "e1", "source_id": "start", "target_id": "end"}]
}`

// runStartEnd runs a START -> END workflow with vars and returns its final variables.
func runStartEnd(t *testing.T, vars map[string]any) map[string]any {
	t.Helper()
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(startEndWorkflowJSON), &def))
	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, vars)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	return instance.WorkflowVariables
}

// A host that runs several top-level workflows for one case starts each with the case ID;
// the engine keeps it.
func TestCaseIDFromHostIsKept(t *testing.T) {
	vars := runStartEnd(t, map[string]any{VarCaseID: "consignment-1"})
	require.Equal(t, "consignment-1", vars[VarCaseID])
}

// A top-level workflow started without a case is its own case.
func TestCaseIDDefaultsToTopLevelWorkflowID(t *testing.T) {
	vars := runStartEnd(t, map[string]any{})
	require.Equal(t, "default-test-workflow-id", vars[VarCaseID])
}
