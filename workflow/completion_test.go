// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// suspendingWorkflowJSON is start -> form -> end, where form's output maps to the variable decision.
const suspendingWorkflowJSON = `
{
  "workflow_id": "suspending",
  "name": "Suspending",
  "version": 1,
  "edges":[
    { "id": "e1", "source_id": "start", "target_id": "form" },
    { "id": "e2", "source_id": "form", "target_id": "end" }
  ],
  "nodes":[
    { "id": "start", "type": "START" },
    { "id": "form", "type": "TASK", "task_template_id": "FORM", "output_mapping": { "decision": "decision" } },
    { "id": "end", "type": "END" }
  ]
}`

// newSuspendingEnv returns a test environment running suspendingWorkflowJSON, whose FORM step
// suspends (its handler returned ErrResultPending), and a pointer to how often the step's Activity ran.
func newSuspendingEnv(t *testing.T) (*testsuite.TestWorkflowEnvironment, WorkflowDefinition, *int) {
	t.Helper()
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(suspendingWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	runs := 0
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "FORM", mock.Anything, mock.Anything, mock.Anything).
		Return(func(context.Context, string, map[string]any, string, ActivationRef) (TaskResult, error) {
			runs++
			return TaskResult{Suspended: true}, nil
		})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	return env, def, &runs
}

// activationOf returns the ActivationID of nodeID's current run.
func activationOf(t *testing.T, env *testsuite.TestWorkflowEnvironment, nodeID string) string {
	t.Helper()
	val, err := env.QueryWorkflow("GetStatus")
	require.NoError(t, err)
	var instance WorkflowInstance
	require.NoError(t, val.Get(&instance))
	return instance.NodeInfo[nodeID].ActivationID
}

// updateOutcome is what became of one CompleteActivation Update.
type updateOutcome struct {
	accepted bool
	rejected error
}

// complete sends the CompleteActivation Update. The workflow decides on it after the calling callback
// returns, so read the outcome once the workflow has run.
func complete(env *testsuite.TestWorkflowEnvironment, updateID, activationID string, output map[string]any) *updateOutcome {
	outcome := &updateOutcome{}
	env.UpdateWorkflow(completeActivationUpdate, updateID, &testsuite.TestUpdateCallback{
		OnAccept:   func() { outcome.accepted = true },
		OnReject:   func(err error) { outcome.rejected = err },
		OnComplete: func(any, error) {},
	}, activationID, output)
	return outcome
}

func requireAccepted(t *testing.T, o *updateOutcome) {
	t.Helper()
	require.NoError(t, o.rejected)
	require.True(t, o.accepted)
}

func requireRejected(t *testing.T, o *updateOutcome) {
	t.Helper()
	require.False(t, o.accepted)
	require.ErrorContains(t, o.rejected, "is not waiting to be completed")
}

func finalVariables(t *testing.T, env *testsuite.TestWorkflowEnvironment) map[string]any {
	t.Helper()
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	return instance.WorkflowVariables
}

// A suspended step waits outside its Activity, and its completion carries the step's output.
func TestSuspendedStepCompletesThroughTheUpdate(t *testing.T) {
	env, def, runs := newSuspendingEnv(t)

	var done *updateOutcome
	env.RegisterDelayedCallback(func() {
		done = complete(env, "u1", activationOf(t, env, "form"), map[string]any{"decision": "approved"})
	}, time.Hour)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	requireAccepted(t, done)
	require.Equal(t, "approved", finalVariables(t, env)["decision"])
	require.Equal(t, 1, *runs)
}

// A step can wait far longer than its Activity's timeout, and its plugin is not run again: the wait
// is not an Activity.
func TestSuspendedStepWaitsPastTheActivityTimeout(t *testing.T) {
	env, def, runs := newSuspendingEnv(t)

	var done *updateOutcome
	env.RegisterDelayedCallback(func() {
		done = complete(env, "u1", activationOf(t, env, "form"), map[string]any{"decision": "approved"})
	}, 400*24*time.Hour)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	requireAccepted(t, done)
	require.Equal(t, "approved", finalVariables(t, env)["decision"])
	require.Equal(t, 1, *runs, "the step's Activity ran once")
}

// A completion for a step that isn't waiting is rejected and changes nothing; of two completions for
// the same step, the first is accepted and the second rejected.
func TestCompletionForAStepNotWaitingIsRejected(t *testing.T) {
	env, def, _ := newSuspendingEnv(t)

	var unknown, first, duplicate *updateOutcome
	env.RegisterDelayedCallback(func() {
		unknown = complete(env, "u1", "no-such-step", map[string]any{"decision": "forged"})
		step := activationOf(t, env, "form")
		first = complete(env, "u2", step, map[string]any{"decision": "approved"})
		duplicate = complete(env, "u3", step, map[string]any{"decision": "again"})
	}, time.Hour)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	requireRejected(t, unknown)
	requireAccepted(t, first)
	requireRejected(t, duplicate)
	require.Equal(t, "approved", finalVariables(t, env)["decision"])
}

// A host can complete a step it suspended before the step's Activity has reported back: the
// completion is accepted and the node takes it once the Activity returns.
func TestCompletionBeforeTheActivityReturnsIsKept(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(suspendingWorkflowJSON), &def))
	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "FORM", mock.Anything, mock.Anything, mock.Anything).
		After(time.Minute).Return(TaskResult{Suspended: true}, nil)
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	var early *updateOutcome
	env.RegisterDelayedCallback(func() {
		early = complete(env, "u1", activationOf(t, env, "form"), map[string]any{"decision": "early"})
	}, time.Second)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	requireAccepted(t, early)
	require.Equal(t, "early", finalVariables(t, env)["decision"])
}

// A step that completed synchronously takes no completion once the workflow has moved on.
func TestCompletionForASynchronousStepIsRejected(t *testing.T) {
	const twoStepJSON = `
	{
	  "workflow_id": "two-step", "name": "Two step", "version": 1,
	  "edges":[
	    { "id": "e1", "source_id": "start", "target_id": "calc" },
	    { "id": "e2", "source_id": "calc", "target_id": "form" },
	    { "id": "e3", "source_id": "form", "target_id": "end" }
	  ],
	  "nodes":[
	    { "id": "start", "type": "START" },
	    { "id": "calc", "type": "TASK", "task_template_id": "CALC" },
	    { "id": "form", "type": "TASK", "task_template_id": "FORM", "output_mapping": { "decision": "decision" } },
	    { "id": "end", "type": "END" }
	  ]
	}`
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(twoStepJSON), &def))
	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "CALC", mock.Anything, mock.Anything, mock.Anything).
		Return(TaskResult{Output: map[string]any{}}, nil)
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "FORM", mock.Anything, mock.Anything, mock.Anything).
		Return(TaskResult{Suspended: true}, nil)
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	var stale, current *updateOutcome
	env.RegisterDelayedCallback(func() {
		stale = complete(env, "u1", activationOf(t, env, "calc"), map[string]any{"decision": "forged"})
		current = complete(env, "u2", activationOf(t, env, "form"), map[string]any{"decision": "approved"})
	}, time.Hour)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	requireRejected(t, stale)
	requireAccepted(t, current)
	require.Equal(t, "approved", finalVariables(t, env)["decision"])
}
