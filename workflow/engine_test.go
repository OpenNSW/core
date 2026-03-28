package engine

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

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

func TestCustomsExportLCLFlow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(customsWorkflowJSON), &def)
	require.NoError(t, err)

	initialWorkflowVariables := make(map[string]any)
	emptyMap := map[string]any{}

	acts := &EngineActivities{}
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

	acts := &EngineActivities{}
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

func TestEdgesAreReturnedInWorkflowInstance(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	var def WorkflowDefinition
	err := json.Unmarshal([]byte(customsWorkflowJSON), &def)
	require.NoError(t, err)

	acts := &EngineActivities{}
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

	acts := &EngineActivities{}
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
