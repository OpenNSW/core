// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	"github.com/OpenNSW/core/shared/maputil"
)

func TestNewTemporalManagerPanicsWithEmptyTaskQueue(t *testing.T) {
	for _, taskQueue := range []string{"", "   "} {
		t.Run(fmt.Sprintf("%q", taskQueue), func(t *testing.T) {
			require.PanicsWithValue(t, "taskQueue must not be empty", func() {
				NewTemporalManager(nil, taskQueue, nil, nil)
			})
		})
	}
}

const customsWorkflowJSON = `
{
  "workflow_id": "customs-export-v1",
  "name": "Customs Export Declaration & Release",
  "version": 1,
  "edges":[
    { "id": "e_customs_start", "source_id": "customs_0_start", "target_id": "customs_1_cusdec_submit" },
    { "id": "e_customs_submit_to_pay", "source_id": "customs_1_cusdec_submit", "target_id": "customs_2_duty_payment" },
    { "id": "e_customs_pay_to_warrant", "source_id": "customs_2_duty_payment", "target_id": "customs_3_warranting_gw" },
    { "id": "e_customs_warrant_lcl", "source_id": "customs_3_warranting_gw", "target_id": "customs_4_lcl_cdn_create", "condition": "consignment_type == 'LCL'" },
    { "id": "e_customs_warrant_fcl", "source_id": "customs_3_warranting_gw", "target_id": "customs_4_fcl_cdn_create", "condition": "consignment_type == 'FCL'" },
    { "id": "e_customs_lcl_ack", "source_id": "customs_4_lcl_cdn_create", "target_id": "customs_5_cdn_ack" },
    { "id": "e_customs_fcl_ack", "source_id": "customs_4_fcl_cdn_create", "target_id": "customs_5_cdn_ack" },
    { "id": "e_customs_ack_bn_create", "source_id": "customs_5_cdn_ack", "target_id": "customs_6_boatnote_create" },
    { "id": "e_customs_bn_create_to_appr", "source_id": "customs_6_boatnote_create", "target_id": "customs_6_boatnote_approve" },
    { "id": "e_customs_bn_done", "source_id": "customs_6_boatnote_approve", "target_id": "customs_7_export_released" }
  ],
  "nodes":[
    { "id": "customs_0_start", "type": "START" },
    { "id": "customs_1_cusdec_submit", "type": "TASK", "task_template_id": "SUBMIT_CUSDEC", "output_mapping": { "consignment_type": "consignment_type" } },
    { "id": "customs_2_duty_payment", "type": "TASK", "task_template_id": "PAY_DUTIES" },
    { "id": "customs_3_warranting_gw", "type": "GATEWAY", "gateway_type": "EXCLUSIVE_SPLIT" },
    { "id": "customs_4_lcl_cdn_create", "type": "TASK", "task_template_id": "CREATE_LCL_CDN" },
    { "id": "customs_4_fcl_cdn_create", "type": "TASK", "task_template_id": "CREATE_FCL_CDN" },
    { "id": "customs_5_cdn_ack", "type": "TASK", "task_template_id": "ACK_CDNS" },
    { "id": "customs_6_boatnote_create", "type": "TASK", "task_template_id": "CREATE_BOAT_NOTE" },
    { "id": "customs_6_boatnote_approve", "type": "TASK", "task_template_id": "APPROVE_BOAT_NOTE" },
    { "id": "customs_7_export_released", "type": "END" }
  ]
}`

const parallelWorkflowJSON = `
{
  "workflow_id": "parallel-test",
  "name": "Parallel Split and Join Test",
  "version": 1,
  "edges":[
    { "id": "e1", "source_id": "start", "target_id": "split" },
    { "id": "e2", "source_id": "split", "target_id": "task_a" },
    { "id": "e3", "source_id": "split", "target_id": "task_b" },
    { "id": "e4", "source_id": "task_a", "target_id": "join" },
    { "id": "e5", "source_id": "task_b", "target_id": "join" },
    { "id": "e6", "source_id": "join", "target_id": "task_c" },
    { "id": "e7", "source_id": "task_c", "target_id": "end" }
  ],
  "nodes":[
    { "id": "start", "type": "START" },
    { "id": "split", "type": "GATEWAY", "gateway_type": "PARALLEL_SPLIT" },
    { "id": "task_a", "type": "TASK", "task_template_id": "TASK_A" },
    { "id": "task_b", "type": "TASK", "task_template_id": "TASK_B" },
    { "id": "join", "type": "GATEWAY", "gateway_type": "PARALLEL_JOIN" },
    { "id": "task_c", "type": "TASK", "task_template_id": "TASK_C" },
    { "id": "end", "type": "END" }
  ]
}`

const inputMappingWorkflowJSON = `
{
	"workflow_id": "input-mapping-test",
	"name": "Input Mapping Test",
	"version": 1,
	"edges":[
		{ "id": "e1", "source_id": "start", "target_id": "task" },
		{ "id": "e2", "source_id": "task", "target_id": "end" }
	],
	"nodes":[
		{ "id": "start", "type": "START" },
		{ "id": "task", "type": "TASK", "task_template_id": "TASK_INPUTS", "input_mapping": { "global_user_email": "local_email" } },
		{ "id": "end", "type": "END" }
	]
}`

const missingInputMappingKeyWorkflowJSON = `
{
	"workflow_id": "missing-input-key-test",
	"name": "Missing Input Mapping Key Test",
	"version": 1,
	"edges":[
		{ "id": "e1", "source_id": "start", "target_id": "task" },
		{ "id": "e2", "source_id": "task", "target_id": "end" }
	],
	"nodes":[
		{ "id": "start", "type": "START" },
		{ "id": "task", "type": "TASK", "task_template_id": "TASK_WITH_MISSING_INPUT", "input_mapping": { "missing_global_var": "local_key" } },
		{ "id": "end", "type": "END" }
	]
}`

const emptyInputMappingWorkflowJSON = `
{
	"workflow_id": "empty-input-mapping-test",
	"name": "Empty Input Mapping Test",
	"version": 1,
	"edges":[
		{ "id": "e1", "source_id": "start", "target_id": "task" },
		{ "id": "e2", "source_id": "task", "target_id": "end" }
	],
	"nodes":[
		{ "id": "start", "type": "START" },
		{ "id": "task", "type": "TASK", "task_template_id": "TASK_EMPTY_INPUTS" },
		{ "id": "end", "type": "END" }
	]
}`

const optionalInputMappingWorkflowJSON = `
{
	"workflow_id": "optional-input-mapping-test",
	"name": "Optional Input Mapping Test",
	"version": 1,
	"edges":[
		{ "id": "e1", "source_id": "start", "target_id": "task" },
		{ "id": "e2", "source_id": "task", "target_id": "end" }
	],
	"nodes":[
		{ "id": "start", "type": "START" },
		{ "id": "task", "type": "TASK", "task_template_id": "TASK_OPTIONAL_INPUTS", "input_mapping": { "global_user_email": "local_email", "global_user_phone?": "local_phone" } },
		{ "id": "end", "type": "END" }
	]
}`

const optionalOutputMappingWorkflowJSON = `
{
	"workflow_id": "optional-output-mapping-test",
	"name": "Optional Output Mapping Test",
	"version": 1,
	"edges":[
		{ "id": "e1", "source_id": "start", "target_id": "task" },
		{ "id": "e2", "source_id": "task", "target_id": "end" }
	],
	"nodes":[
		{ "id": "start", "type": "START" },
		{ "id": "task", "type": "TASK", "task_template_id": "TASK_OPTIONAL_OUTPUTS", "output_mapping": { "task_email": "global_user_email", "task_phone?": "global_user_phone" } },
		{ "id": "end", "type": "END" }
	]
}`

const missingRequiredOutputWorkflowJSON = `
{
	"workflow_id": "missing-required-output-test",
	"name": "Missing Required Output Test",
	"version": 1,
	"edges":[
		{ "id": "e1", "source_id": "start", "target_id": "task" },
		{ "id": "e2", "source_id": "task", "target_id": "end" }
	],
	"nodes":[
		{ "id": "start", "type": "START" },
		{ "id": "task", "type": "TASK", "task_template_id": "TASK_MISSING_REQUIRED_OUTPUT", "output_mapping": { "task_phone": "global_user_phone" } },
		{ "id": "end", "type": "END" }
	]
}`

const nodeOutputToSubsetInputWorkflowJSON = `
{
	"workflow_id": "subset-input-mapping-test",
	"name": "Subset Input Mapping Test",
	"version": 1,
	"edges":[
		{ "id": "e1", "source_id": "start", "target_id": "node1" },
		{ "id": "e2", "source_id": "node1", "target_id": "node2" },
		{ "id": "e3", "source_id": "node2", "target_id": "end" }
	],
	"nodes":[
		{ "id": "start", "type": "START" },
		{ "id": "node1", "type": "TASK", "task_template_id": "NODE1_TASK", "output_mapping": { "task_email": "global_user_email", "task_phone": "global_user_phone" } },
		{ "id": "node2", "type": "TASK", "task_template_id": "NODE2_TASK", "input_mapping": { "global_user_email": "local_email" } },
		{ "id": "end", "type": "END" }
	]
}`

func TestCustomsExportLCLFlow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(customsWorkflowJSON), &def)
	require.NoError(t, err)

	initialWorkflowVariables := make(map[string]any)
	emptyMap := map[string]any{}

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "SUBMIT_CUSDEC", mock.Anything).
		Return(map[string]any{"consignment_type": "LCL"}, nil).Once()

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "PAY_DUTIES", mock.Anything).
		Return(emptyMap, nil).Once()

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "CREATE_LCL_CDN", mock.Anything).
		Return(emptyMap, nil).Once()

	// CREATE_FCL_CDN should NEVER be called since the LCL path was evaluated.
	env.AssertNotCalled(t, "ExecuteTaskActivity", mock.Anything, "CREATE_FCL_CDN", mock.Anything)

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "ACK_CDNS", mock.Anything).
		Return(emptyMap, nil).Once()

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "CREATE_BOAT_NOTE", mock.Anything).
		Return(emptyMap, nil).Once()

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "APPROVE_BOAT_NOTE", mock.Anything).
		Return(emptyMap, nil).Once()

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialWorkflowVariables)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	err = env.GetWorkflowResult(&instance)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, instance.Status)
	require.Equal(t, "LCL", instance.WorkflowVariables["consignment_type"])

	env.AssertExpectations(t)
}

func TestParallelJoinFlow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(parallelWorkflowJSON), &def)
	require.NoError(t, err)

	initialWorkflowVariables := make(map[string]any)
	emptyMap := map[string]any{}

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_A", mock.Anything).
		Return(emptyMap, nil).Once()

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_B", mock.Anything).
		Return(emptyMap, nil).Once()

	// TASK_C must only be called ONCE to prove join synchronization works
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_C", mock.Anything).
		Return(emptyMap, nil).Once()

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialWorkflowVariables)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	err = env.GetWorkflowResult(&instance)
	require.NoError(t, err)

	require.Equal(t, StatusCompleted, instance.Status)

	env.AssertExpectations(t)
}

func TestTaskNodeAppliesInputMapping(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(inputMappingWorkflowJSON), &def)
	require.NoError(t, err)

	initialWorkflowVariables := map[string]any{
		"global_user_email": "user@example.com",
		"global_user_name":  "Alice",
	}

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_INPUTS", mock.MatchedBy(func(inputs map[string]any) bool {
		if len(inputs) != 1 {
			return false
		}
		value, exists := inputs["local_email"]
		return exists && value == "user@example.com"
	})).Return(map[string]any{}, nil).Once()

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialWorkflowVariables)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestTaskNodeWithEmptyInputMappingPassesNoInputs(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(emptyInputMappingWorkflowJSON), &def)
	require.NoError(t, err)

	initialWorkflowVariables := map[string]any{
		"global_user_email": "user@example.com",
		"global_user_name":  "Alice",
	}

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_EMPTY_INPUTS", mock.MatchedBy(func(inputs map[string]any) bool {
		return len(inputs) == 0
	})).Return(map[string]any{}, nil).Once()

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialWorkflowVariables)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestTaskNodeFailsWhenInputKeyMissing(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(missingInputMappingKeyWorkflowJSON), &def)
	require.NoError(t, err)

	initialWorkflowVariables := map[string]any{
		"global_user_email": "user@example.com",
	}

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	// The input mapping error now parks the node for admin intervention instead of
	// failing the workflow outright. Abort it to reproduce today's end-state.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "task",
			Action: AdminActionAbort,
		})
	}, time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialWorkflowVariables)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Contains(t, env.GetWorkflowError().Error(), "input mapping error")
	require.Contains(t, env.GetWorkflowError().Error(), "missing_global_var")
}

func TestNodeOutputFlowsIntoSubsetInputMapping(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(nodeOutputToSubsetInputWorkflowJSON), &def)
	require.NoError(t, err)

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "NODE1_TASK", mock.Anything).
		Return(map[string]any{
			"task_email": "user@example.com",
			"task_phone": "+123456789",
		}, nil).Once()

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "NODE2_TASK", mock.MatchedBy(func(inputs map[string]any) bool {
		if len(inputs) != 1 {
			return false
		}
		value, exists := inputs["local_email"]
		return exists && value == "user@example.com"
	})).Return(map[string]any{}, nil).Once()

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	err = env.GetWorkflowResult(&instance)
	require.NoError(t, err)
	require.Equal(t, "user@example.com", instance.WorkflowVariables["global_user_email"])
	require.Equal(t, "+123456789", instance.WorkflowVariables["global_user_phone"])

	env.AssertExpectations(t)
}

func TestTaskNodeSkipsOptionalInputWhenMissing(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(optionalInputMappingWorkflowJSON), &def)
	require.NoError(t, err)

	initialWorkflowVariables := map[string]any{
		"global_user_email": "user@example.com",
	}

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_OPTIONAL_INPUTS", mock.MatchedBy(func(inputs map[string]any) bool {
		if len(inputs) != 1 {
			return false
		}
		value, exists := inputs["local_email"]
		return exists && value == "user@example.com"
	})).Return(map[string]any{}, nil).Once()

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialWorkflowVariables)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestTaskNodeAppliesOptionalInputWhenPresent(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(optionalInputMappingWorkflowJSON), &def)
	require.NoError(t, err)

	initialWorkflowVariables := map[string]any{
		"global_user_email": "user@example.com",
		"global_user_phone": "+123456789",
	}

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_OPTIONAL_INPUTS", mock.MatchedBy(func(inputs map[string]any) bool {
		if len(inputs) != 2 {
			return false
		}
		email, emailOk := inputs["local_email"]
		phone, phoneOk := inputs["local_phone"]
		return emailOk && email == "user@example.com" && phoneOk && phone == "+123456789"
	})).Return(map[string]any{}, nil).Once()

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialWorkflowVariables)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestTaskNodeSkipsOptionalOutputWhenMissing(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(optionalOutputMappingWorkflowJSON), &def)
	require.NoError(t, err)

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_OPTIONAL_OUTPUTS", mock.Anything).
		Return(map[string]any{"task_email": "user@example.com"}, nil).Once()

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	err = env.GetWorkflowResult(&instance)
	require.NoError(t, err)
	require.Equal(t, "user@example.com", instance.WorkflowVariables["global_user_email"])
	_, phoneExists := instance.WorkflowVariables["global_user_phone"]
	require.False(t, phoneExists, "global_user_phone should not be set when optional output is missing")

	env.AssertExpectations(t)
}

func TestTaskNodeFailsWhenRequiredOutputMissing(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(missingRequiredOutputWorkflowJSON), &def)
	require.NoError(t, err)

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_MISSING_REQUIRED_OUTPUT", mock.Anything).
		Return(map[string]any{}, nil).Once()

	// The output mapping error now parks the node for admin intervention instead of
	// failing the workflow outright. Abort it to reproduce today's end-state.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "task",
			Action: AdminActionAbort,
		})
	}, 100*time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Contains(t, env.GetWorkflowError().Error(), "output mapping error")
	require.Contains(t, env.GetWorkflowError().Error(), "task_phone")
}

func TestEdgesAreReturnedInWorkflowInstance(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(customsWorkflowJSON), &def)
	require.NoError(t, err)

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	// Critical: ensure condition variable exists
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "SUBMIT_CUSDEC", mock.Anything).
		Return(map[string]any{"consignment_type": "LCL"}, nil)

	// fallback
	env.OnActivity("ExecuteTaskActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(map[string]any{}, nil)

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	err = env.GetWorkflowResult(&instance)
	require.NoError(t, err)

	require.NotNil(t, instance.Edges)
	require.Len(t, instance.Edges, len(def.Edges))

	nodeIDMap := make(map[string]string)
	for defNodeID, nodeInfo := range instance.NodeInfo {
		nodeIDMap[defNodeID] = nodeInfo.ID
	}
	for i, edge := range instance.Edges {
		require.Equal(t, def.Edges[i].ID, edge.ID)
		require.Equal(t, nodeIDMap[def.Edges[i].SourceID], edge.SourceID)
		require.Equal(t, nodeIDMap[def.Edges[i].TargetID], edge.TargetID)
	}
}

func TestEdgesReferenceValidNodeInstanceIDs(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(parallelWorkflowJSON), &def)
	require.NoError(t, err)

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(map[string]any{}, nil)

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	var instance WorkflowInstance
	err = env.GetWorkflowResult(&instance)
	require.NoError(t, err)

	// Build set of valid node instance IDs
	validNodeIDs := make(map[string]bool)
	for _, node := range instance.NodeInfo {
		validNodeIDs[node.ID] = true
	}

	// Validate all edges reference valid nodes
	for _, edge := range instance.Edges {
		require.True(t, validNodeIDs[edge.SourceID], "invalid sourceID: %s", edge.SourceID)
		require.True(t, validNodeIDs[edge.TargetID], "invalid targetID: %s", edge.TargetID)
	}
}

func TestInvalidEdgeDefinitionFailsWorkflow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Broken edge (invalid target_id)
	badJSON := `
	{
	  "workflow_id": "bad",
	  "name": "bad",
	  "version": 1,
	  "edges":[
	    { "id": "e1", "source_id": "start", "target_id": "missing" }
	  ],
	  "nodes":[
	    { "id": "start", "type": "START" }
	  ]
	}`

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(badJSON), &def)
	require.NoError(t, err)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

func TestEmitSignalStandaloneWarns(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	emitSignalJSON := `
	{
		"workflow_id": "emit-signal-standalone-test",
		"name": "Emit Signal Standalone Test",
		"version": 1,
		"edges":[
			{ "id": "e1", "source_id": "start", "target_id": "emit" },
			{ "id": "e2", "source_id": "emit", "target_id": "end" }
		],
		"nodes":[
			{ "id": "start", "type": "START" },
			{
				"id": "emit",
				"type": "TASK",
				"task_template_id": "sys:emit_signal",
				"input_mapping": {
					"sig_name": "signal_name",
					"sig_payload": "payload"
				}
			},
			{ "id": "end", "type": "END" }
		]
	}`

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(emitSignalJSON), &def)
	require.NoError(t, err)

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	initialVars := map[string]any{
		"sig_name": "my_signal",
		"sig_payload": map[string]any{
			"key": "value",
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	err = env.GetWorkflowResult(&instance)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, instance.Status)
}

func TestEmitSignalInvalidPayloadType(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	emitSignalJSON := `
	{
		"workflow_id": "emit-signal-invalid-payload-test",
		"name": "Emit Signal Invalid Payload Test",
		"version": 1,
		"edges":[
			{ "id": "e1", "source_id": "start", "target_id": "emit" },
			{ "id": "e2", "source_id": "emit", "target_id": "end" }
		],
		"nodes":[
			{ "id": "start", "type": "START" },
			{
				"id": "emit",
				"type": "TASK",
				"task_template_id": "sys:emit_signal",
				"input_mapping": {
					"sig_name": "signal_name",
					"sig_payload": "payload"
				}
			},
			{ "id": "end", "type": "END" }
		]
	}`

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(emitSignalJSON), &def)
	require.NoError(t, err)

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	initialVars := map[string]any{
		"sig_name":    "my_signal",
		"sig_payload": "invalid-payload-string", // string is not map[string]any
	}

	// Verify that the node parks on the type mismatch error, then send Abort to fail the workflow
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow("GetStatus")
		require.NoError(t, err)
		var instance WorkflowInstance
		require.NoError(t, val.Get(&instance))
		require.Equal(t, NodeStatusAwaitingAdmin, instance.NodeInfo["emit"].Status)
		require.Contains(t, instance.NodeInfo["emit"].LastError, "emit_signal task payload must be a map[string]any, got string")

		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "emit",
			Action: AdminActionAbort,
			Reason: "failing on bad payload type",
		})
	}, time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Contains(t, env.GetWorkflowError().Error(), "emit_signal task payload must be a map[string]any, got string")
}

func TestEmitSignalAuditTrailOnFailure(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	emitSignalJSON := `
	{
		"workflow_id": "emit-signal-fail-test",
		"name": "Emit Signal Fail Test",
		"version": 1,
		"edges":[
			{ "id": "e1", "source_id": "start", "target_id": "emit" },
			{ "id": "e2", "source_id": "emit", "target_id": "end" }
		],
		"nodes":[
			{ "id": "start", "type": "START" },
			{
				"id": "emit",
				"type": "TASK",
				"task_template_id": "sys:emit_signal",
				"input_mapping": {
					"sig_name": "signal_name",
					"sig_payload": "payload"
				}
			},
			{ "id": "end", "type": "END" }
		]
	}`

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(emitSignalJSON), &def)
	require.NoError(t, err)

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	// Mock SignalExternalWorkflow to fail for non-existent-parent
	env.OnSignalExternalWorkflow(
		"default-test-namespace",
		"non-existent-parent",
		"",
		"child_broadcast_signal",
		mock.Anything,
	).Return(fmt.Errorf("workflow not found")).Once()

	initialVars := map[string]any{
		"sig_name": "my_signal",
		"sig_payload": map[string]any{
			"key": "value",
		},
		// Set _parent_workflow_id to a non-existent parent so the emission fails
		"_parent_workflow_id": "non-existent-parent",
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	err = env.GetWorkflowResult(&instance)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, instance.Status)

	// Verify that the AuditTrail contains the signal sending failure log
	auditLogged := false
	for _, entry := range instance.AuditTrail {
		if strings.Contains(entry, "emit_signal: failed to send signal to parent non-existent-parent:") {
			auditLogged = true
			break
		}
	}
	require.True(t, auditLogged, "expected signal failure to be logged to AuditTrail, got entries: %v", instance.AuditTrail)
}

// timerPollWorkflowJSON models the trader-free polling loop: poll, and while the
// result is not delivered, wait on a TIMER and poll again — bounded by an
// attempt cap so a never-delivered envelope cannot poll forever.
//
//	poll -> gw_poll -- delivered --------> end_delivered
//	                \- not delivered ----> wait (TIMER) -> gw_cap -- under cap --> poll
//	                                                              \- at cap ----> end_timeout
const timerPollWorkflowJSON = `
{
  "workflow_id": "timer-poll-v1",
  "name": "Timed polling loop",
  "version": 1,
  "edges":[
    { "id": "e_start", "source_id": "start", "target_id": "poll" },
    { "id": "e_poll_gw", "source_id": "poll", "target_id": "gw_poll" },
    { "id": "e_delivered", "source_id": "gw_poll", "target_id": "end_delivered", "condition": "delivered == true" },
    { "id": "e_not_delivered", "source_id": "gw_poll", "target_id": "wait", "condition": "delivered != true" },
    { "id": "e_wait_cap", "source_id": "wait", "target_id": "gw_cap" },
    { "id": "e_retry", "source_id": "gw_cap", "target_id": "poll", "condition": "poll.attempts < 3" },
    { "id": "e_giveup", "source_id": "gw_cap", "target_id": "end_timeout", "condition": "poll.attempts >= 3" }
  ],
  "nodes":[
    { "id": "start", "type": "START" },
    { "id": "poll", "type": "TASK", "task_template_id": "POLL_HUB", "output_mapping": { "delivered": "delivered" } },
    { "id": "gw_poll", "type": "GATEWAY", "gateway_type": "EXCLUSIVE_SPLIT" },
    { "id": "wait", "type": "TIMER", "timer": { "duration": "1m", "counter_key": "poll.attempts" } },
    { "id": "gw_cap", "type": "GATEWAY", "gateway_type": "EXCLUSIVE_SPLIT" },
    { "id": "end_delivered", "type": "END" },
    { "id": "end_timeout", "type": "END" }
  ]
}`

func TestTimerNodeLoopsUntilDelivered(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(timerPollWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	// Not delivered twice, then delivered on the third poll.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "POLL_HUB", mock.Anything).
		Return(map[string]any{"delivered": false}, nil).Twice()
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "POLL_HUB", mock.Anything).
		Return(map[string]any{"delivered": true}, nil).Once()
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	start := env.Now()
	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Equal(t, StatusCompleted, instance.Status)
	require.Equal(t, true, instance.WorkflowVariables["delivered"])

	// Two waits happened (after the two undelivered polls), so the counter is 2
	// and the workflow clock advanced by two minutes rather than busy-looping.
	attempts, ok := maputil.GetNestedKey(instance.WorkflowVariables, "poll.attempts")
	require.True(t, ok, "timer should publish its fire count")
	require.Equal(t, 2, asInt(attempts))
	require.Equal(t, 2*time.Minute, env.Now().Sub(start))

	env.AssertExpectations(t)
}

func TestTimerNodeStopsAtAttemptCap(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(timerPollWorkflowJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	// Never delivered: the cap must stop the loop at exactly 3 polls.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "POLL_HUB", mock.Anything).
		Return(map[string]any{"delivered": false}, nil).Times(3)
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	require.Equal(t, StatusCompleted, instance.Status)

	attempts, ok := maputil.GetNestedKey(instance.WorkflowVariables, "poll.attempts")
	require.True(t, ok)
	require.Equal(t, 3, asInt(attempts), "loop must stop at the cap, not run forever")
	require.Equal(t, NodeStatusCompleted, instance.NodeInfo["end_timeout"].Status)
	require.Equal(t, NodeStatusNotStarted, instance.NodeInfo["end_delivered"].Status)

	env.AssertExpectations(t)
}

func TestTimerNodeRejectsBadConfig(t *testing.T) {
	for _, tc := range []struct{ name, timerJSON, wantErr string }{
		{"missing timer config", `{ "id": "wait", "type": "TIMER" }`, "timer.duration is required"},
		{"empty duration", `{ "id": "wait", "type": "TIMER", "timer": { "duration": "" } }`, "timer.duration is required"},
		{"unparseable duration", `{ "id": "wait", "type": "TIMER", "timer": { "duration": "soon" } }`, "invalid timer.duration"},
		{"zero duration", `{ "id": "wait", "type": "TIMER", "timer": { "duration": "0s" } }`, "must be positive"},
		{"negative duration", `{ "id": "wait", "type": "TIMER", "timer": { "duration": "-1m" } }`, "must be positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testSuite := &testsuite.WorkflowTestSuite{}
			env := testSuite.NewTestWorkflowEnvironment()

			defJSON := `{
              "workflow_id": "timer-bad", "name": "bad timer", "version": 1,
              "edges":[
                { "id": "e1", "source_id": "start", "target_id": "wait" },
                { "id": "e2", "source_id": "wait", "target_id": "end" }
              ],
              "nodes":[
                { "id": "start", "type": "START" },
                ` + tc.timerJSON + `,
                { "id": "end", "type": "END" }
              ]
            }`

			var def WorkflowDefinition
			require.NoError(t, json.Unmarshal([]byte(defJSON), &def))

			acts := &Activities{}
			env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
			env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

			// A misconfigured node parks for admin rather than failing outright,
			// so the config error surfaces as LastError on the parked node.
			env.RegisterDelayedCallback(func() {
				val, err := env.QueryWorkflow("GetStatus")
				require.NoError(t, err)
				var instance WorkflowInstance
				require.NoError(t, val.Get(&instance))
				require.Equal(t, NodeStatusAwaitingAdmin, instance.NodeInfo["wait"].Status)
				require.Contains(t, instance.NodeInfo["wait"].LastError, tc.wantErr)
			}, time.Millisecond)

			// Abort re-raises the original error so it also reaches the caller.
			env.RegisterDelayedCallback(func() {
				env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
					NodeID: "wait",
					Action: AdminActionAbort,
				})
			}, 2*time.Millisecond)

			env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

			require.True(t, env.IsWorkflowCompleted())
			err := env.GetWorkflowError()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestTimerNodeRequiresExactlyOneOutgoingEdge(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Two outgoing edges: branching belongs to a gateway, not a timer.
	defJSON := `{
      "workflow_id": "timer-fanout", "name": "timer fanout", "version": 1,
      "edges":[
        { "id": "e1", "source_id": "start", "target_id": "wait" },
        { "id": "e2", "source_id": "wait", "target_id": "end_a" },
        { "id": "e3", "source_id": "wait", "target_id": "end_b" }
      ],
      "nodes":[
        { "id": "start", "type": "START" },
        { "id": "wait", "type": "TIMER", "timer": { "duration": "1m" } },
        { "id": "end_a", "type": "END" },
        { "id": "end_b", "type": "END" }
      ]
    }`

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(defJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdminResolutionSignalName, AdminResolutionSignal{
			NodeID: "wait",
			Action: AdminActionAbort,
		})
	}, time.Millisecond)

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected exactly 1 outgoing edge, got 2")
}

func TestTimerNodeDefaultsCounterKeyToNodeID(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	defJSON := `{
      "workflow_id": "timer-default-key", "name": "default counter key", "version": 1,
      "edges":[
        { "id": "e1", "source_id": "start", "target_id": "wait" },
        { "id": "e2", "source_id": "wait", "target_id": "end" }
      ],
      "nodes":[
        { "id": "start", "type": "START" },
        { "id": "wait", "type": "TIMER", "timer": { "duration": "90s" } },
        { "id": "end", "type": "END" }
      ]
    }`

	var def WorkflowDefinition
	require.NoError(t, json.Unmarshal([]byte(defJSON), &def))

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	start := env.Now()
	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var instance WorkflowInstance
	require.NoError(t, env.GetWorkflowResult(&instance))
	got, ok := maputil.GetNestedKey(instance.WorkflowVariables, "wait.iterations")
	require.True(t, ok, "counter should default to <node id>.iterations")
	require.Equal(t, 1, asInt(got))
	require.Equal(t, 90*time.Second, env.Now().Sub(start))
}
