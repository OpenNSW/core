// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type BatchGatewayTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
}

func TestBatchGatewayTestSuite(t *testing.T) {
	suite.Run(t, new(BatchGatewayTestSuite))
}

// --- Test 1: Basic 3-item, 2-partition split+join ---

func (s *BatchGatewayTestSuite) TestBatchSplit_Basic_TwoPartitions() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	// The workflow: start → load_items → BATCH_SPLIT → (food_inspect | goods_inspect) → BATCH_JOIN → issue_cert → end
	masterDef := WorkflowDefinition{
		ID:   "basic_batch_test",
		Name: "Basic Batch Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "load_items", Type: NodeTypeTask, TaskTemplateID: "LOAD_ITEMS",
				OutputMapping: map[string]string{"items": "commodities"}},
			{ID: "gw_type", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{ItemsVariable: "commodities", IDField: "commodity_id"}},
			{ID: "food_inspect", Type: NodeTypeTask, TaskTemplateID: "FOOD_INSPECTION"},
			{ID: "goods_inspect", Type: NodeTypeTask, TaskTemplateID: "GOODS_INSPECTION"},
			{ID: "join_type", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_type", ItemsVariable: "commodities", IDField: "commodity_id"}},
			{ID: "issue_cert", Type: NodeTypeTask, TaskTemplateID: "ISSUE_CERTIFICATE"},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "load_items"},
			{ID: "e2", SourceID: "load_items", TargetID: "gw_type"},
			{ID: "e3", SourceID: "gw_type", TargetID: "food_inspect", Condition: `item.type == "food"`},
			{ID: "e4", SourceID: "gw_type", TargetID: "goods_inspect", Condition: `item.type == "goods"`},
			{ID: "e5", SourceID: "food_inspect", TargetID: "join_type"},
			{ID: "e6", SourceID: "goods_inspect", TargetID: "join_type"},
			{ID: "e7", SourceID: "join_type", TargetID: "issue_cert"},
			{ID: "e8", SourceID: "issue_cert", TargetID: "end"},
		},
	}

	// WorkflowCompletedActivity: top-level calls succeed, child calls (with "--") are ignored.
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found in host registry", workflowID)
			}
			return nil
		})

	// load_items returns 3 items.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "LOAD_ITEMS", mock.Anything).
		Return(map[string]any{
			"items": []any{
				map[string]any{"commodity_id": "C1", "type": "food", "description": "Dried fish"},
				map[string]any{"commodity_id": "C2", "type": "goods", "description": "Textiles"},
				map[string]any{"commodity_id": "C3", "type": "food", "description": "Spices"},
			},
		}, nil).Once()

	// food_inspect: called in the "food" child workflow, processes 2 items.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "FOOD_INSPECTION", mock.Anything).
		Return(map[string]any{}, nil)

	// goods_inspect: called in the "goods" child workflow, processes 1 item.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "GOODS_INSPECTION", mock.Anything).
		Return(map[string]any{}, nil)

	// issue_cert: called in the parent after join, sees all 3 items.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "ISSUE_CERTIFICATE", mock.Anything).
		Return(map[string]any{}, nil)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "batch-test-1"})

	env.ExecuteWorkflow(GraphInterpreterWorkflow, masterDef, map[string]any{})

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal(StatusCompleted, result.Status)

	// Verify all 3 items are present in the merged result.
	items, ok := result.WorkflowVariables["commodities"].([]any)
	s.True(ok, "commodities should be a []any after merge")
	s.Len(items, 3, "all 3 items should be reunified after batch join")
}

// --- Test 2: Single item traversal ---

func (s *BatchGatewayTestSuite) TestBatchSplit_SingleItem() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "single_item_batch",
		Name: "Single Item Batch",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}}, // defaults: _items, id
			{ID: "process", Type: NodeTypeTask, TaskTemplateID: "PROCESS"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "process", Condition: `item.needsWork == true`},
			{ID: "e3", SourceID: "process", TargetID: "gw_join"},
			{ID: "e4", SourceID: "gw_join", TargetID: "end"},
		},
	}

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found", workflowID)
			}
			return nil
		})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "PROCESS", mock.Anything).
		Return(map[string]any{}, nil)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "single-item-1"})

	initialVars := map[string]any{
		"_items": []any{
			map[string]any{"id": "item-1", "needsWork": true},
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal(StatusCompleted, result.Status)

	items, ok := result.WorkflowVariables["_items"].([]any)
	s.True(ok)
	s.Len(items, 1, "single item should be preserved")
}

// --- Test 3: Unmatched items with no default → error ---

func (s *BatchGatewayTestSuite) TestBatchSplit_UnmatchedItemNoDefault_Fails() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "unmatched_test",
		Name: "Unmatched Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "only_food", Type: NodeTypeTask, TaskTemplateID: "FOOD_ONLY"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			// Only a "food" edge — no default.
			{ID: "e2", SourceID: "gw_split", TargetID: "only_food", Condition: `item.type == "food"`},
			{ID: "e3", SourceID: "only_food", TargetID: "gw_join"},
			{ID: "e4", SourceID: "gw_join", TargetID: "end"},
		},
	}

	// Send an admin abort signal so the workflow fails instead of hanging in admin-park.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("AdminResolutionSignal", AdminResolutionSignal{
			NodeID: "gw_split",
			Action: AdminActionAbort,
		})
	}, 0)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "unmatched-1"})

	// Item "C2" is type "goods" — won't match the "food" edge.
	initialVars := map[string]any{
		"_items": []any{
			map[string]any{"id": "C1", "type": "food"},
			map[string]any{"id": "C2", "type": "goods"},
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "unmatched")
	s.Contains(err.Error(), "C2")
}

// --- Test 3b: Missing item ID → error ---

func (s *BatchGatewayTestSuite) TestBatchSplit_MissingItemID_Fails() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "missing_id_test",
		Name: "Missing ID Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "process", Type: NodeTypeTask, TaskTemplateID: "PROCESS"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "process"},
			{ID: "e3", SourceID: "process", TargetID: "gw_join"},
			{ID: "e4", SourceID: "gw_join", TargetID: "end"},
		},
	}

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("AdminResolutionSignal", AdminResolutionSignal{
			NodeID: "gw_split",
			Action: AdminActionAbort,
		})
	}, 0)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "missing-id-1"})

	// Second item is missing the "id" field
	initialVars := map[string]any{
		"_items": []any{
			map[string]any{"id": "C1", "type": "food"},
			map[string]any{"type": "goods"},
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "missing required ID field")
}

// --- Test 3c: Duplicate item ID → error ---

func (s *BatchGatewayTestSuite) TestBatchSplit_DuplicateItemID_Fails() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "dup_id_test",
		Name: "Duplicate ID Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "process", Type: NodeTypeTask, TaskTemplateID: "PROCESS"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "process"},
			{ID: "e3", SourceID: "process", TargetID: "gw_join"},
			{ID: "e4", SourceID: "gw_join", TargetID: "end"},
		},
	}

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("AdminResolutionSignal", AdminResolutionSignal{
			NodeID: "gw_split",
			Action: AdminActionAbort,
		})
	}, 0)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "dup-id-1"})

	// Duplicate "C1" ID
	initialVars := map[string]any{
		"_items": []any{
			map[string]any{"id": "C1", "type": "food"},
			map[string]any{"id": "C1", "type": "goods"},
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "duplicate item ID \"C1\"")
}

// --- Test 4: Default edge catches unmatched items ---

func (s *BatchGatewayTestSuite) TestBatchSplit_DefaultEdgeCatchAll() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "default_edge_test",
		Name: "Default Edge Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "food_process", Type: NodeTypeTask, TaskTemplateID: "FOOD_PROCESS"},
			{ID: "other_process", Type: NodeTypeTask, TaskTemplateID: "OTHER_PROCESS"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "food_process", Condition: `item.type == "food"`},
			{ID: "e3", SourceID: "gw_split", TargetID: "other_process"}, // Default: no condition
			{ID: "e4", SourceID: "food_process", TargetID: "gw_join"},
			{ID: "e5", SourceID: "other_process", TargetID: "gw_join"},
			{ID: "e6", SourceID: "gw_join", TargetID: "end"},
		},
	}

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found", workflowID)
			}
			return nil
		})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "FOOD_PROCESS", mock.Anything).
		Return(map[string]any{}, nil)
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "OTHER_PROCESS", mock.Anything).
		Return(map[string]any{}, nil)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "default-edge-1"})

	initialVars := map[string]any{
		"_items": []any{
			map[string]any{"id": "C1", "type": "food"},
			map[string]any{"id": "C2", "type": "goods"},    // no "food" match → default
			map[string]any{"id": "C3", "type": "chemical"}, // no "food" match → default
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal(StatusCompleted, result.Status)

	items, ok := result.WorkflowVariables["_items"].([]any)
	s.True(ok)
	s.Len(items, 3, "all 3 items should be present after merge")
}

// --- Test 5: Empty items slice → skip to join ---

func (s *BatchGatewayTestSuite) TestBatchSplit_EmptyItems_SkipsToJoin() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "empty_items_test",
		Name: "Empty Items Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "process", Type: NodeTypeTask, TaskTemplateID: "SHOULD_NOT_RUN"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "final_task", Type: NodeTypeTask, TaskTemplateID: "FINAL"},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "process", Condition: `item.active == true`},
			{ID: "e3", SourceID: "process", TargetID: "gw_join"},
			{ID: "e4", SourceID: "gw_join", TargetID: "final_task"},
			{ID: "e5", SourceID: "final_task", TargetID: "end"},
		},
	}

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "FINAL", mock.Anything).
		Return(map[string]any{}, nil)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "empty-items-1"})

	initialVars := map[string]any{
		"_items": []any{}, // Empty
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal(StatusCompleted, result.Status)
}

// --- Test 6: Validation — BATCH_SPLIT without paired BATCH_JOIN ---

func (s *BatchGatewayTestSuite) TestBatchValidation_MissingJoin_Fails() {
	def := WorkflowDefinition{
		ID:   "missing_join_test",
		Name: "Missing Join",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "end"},
		},
	}

	err := ValidateBatchGateways(def)
	s.Error(err)
	s.Contains(err.Error(), "no paired BATCH_JOIN")
}

// --- Test 7: Validation — BATCH_JOIN references non-existent BATCH_SPLIT ---

func (s *BatchGatewayTestSuite) TestBatchValidation_JoinReferencesNonexistentSplit_Fails() {
	def := WorkflowDefinition{
		ID:   "bad_ref_test",
		Name: "Bad Reference",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "nonexistent"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_join"},
			{ID: "e2", SourceID: "gw_join", TargetID: "end"},
		},
	}

	err := ValidateBatchGateways(def)
	s.Error(err)
	s.Contains(err.Error(), "non-existent BATCH_SPLIT")
}

// --- Test 7b: Validation — BATCH_JOIN with multiple outgoing edges ---

func (s *BatchGatewayTestSuite) TestBatchValidation_JoinMultipleOutgoingEdges_Fails() {
	def := WorkflowDefinition{
		ID:   "multi_out_join_test",
		Name: "Multi Out Join",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "task_a", Type: NodeTypeTask, TaskTemplateID: "TASK_A"},
			{ID: "task_b", Type: NodeTypeTask, TaskTemplateID: "TASK_B"},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "gw_join"},
			// Multiple outgoing edges directly from BATCH_JOIN:
			{ID: "e3", SourceID: "gw_join", TargetID: "task_a"},
			{ID: "e4", SourceID: "gw_join", TargetID: "task_b"},
			{ID: "e5", SourceID: "task_a", TargetID: "end"},
			{ID: "e6", SourceID: "task_b", TargetID: "end"},
		},
	}

	err := ValidateBatchGateways(def)
	s.Error(err)
	s.Contains(err.Error(), "cannot have more than 1 outgoing edge")
}

// --- Test 7c: Validation — edge escaping batch region to post-join node ---

func (s *BatchGatewayTestSuite) TestBatchValidation_EscapesBatchRegionToPostJoin_Fails() {
	def := WorkflowDefinition{
		ID:   "leak_to_post_join_test",
		Name: "Leak To Post Join",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "process", Type: NodeTypeTask, TaskTemplateID: "PROCESS"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "issue_cert", Type: NodeTypeTask, TaskTemplateID: "ISSUE_CERT"},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "process"},
			{ID: "e3", SourceID: "process", TargetID: "gw_join"},
			// Leak: edge bypassing gw_join to post-join node issue_cert
			{ID: "e_leak", SourceID: "process", TargetID: "issue_cert"},
			{ID: "e4", SourceID: "gw_join", TargetID: "issue_cert"},
			{ID: "e5", SourceID: "issue_cert", TargetID: "end"},
		},
	}

	err := ValidateBatchGateways(def)
	s.Error(err)
	// issue_cert / end are reachable from gw_split without passing through gw_join and cannot reach gw_join
	s.True(strings.Contains(err.Error(), "cannot reach paired BATCH_JOIN") ||
		strings.Contains(err.Error(), "without passing through paired BATCH_JOIN"))
}

// --- Test 7d: Validation — edge escaping batch region directly to END ---

func (s *BatchGatewayTestSuite) TestBatchValidation_EscapesBatchRegionToEnd_Fails() {
	def := WorkflowDefinition{
		ID:   "leak_to_end_test",
		Name: "Leak To End",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "process", Type: NodeTypeTask, TaskTemplateID: "PROCESS"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "process"},
			{ID: "e3", SourceID: "process", TargetID: "gw_join"},
			// Leak: edge bypassing gw_join directly to END
			{ID: "e_leak", SourceID: "process", TargetID: "end"},
			{ID: "e4", SourceID: "gw_join", TargetID: "end"},
		},
	}

	err := ValidateBatchGateways(def)
	s.Error(err)
	s.Contains(err.Error(), "without passing through paired BATCH_JOIN")
}

// --- Test 7e: Validation — dead-end node inside batch region ---

func (s *BatchGatewayTestSuite) TestBatchValidation_InternalDeadEndNode_Fails() {
	def := WorkflowDefinition{
		ID:   "dead_end_test",
		Name: "Dead End Inside Batch",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "process", Type: NodeTypeTask, TaskTemplateID: "PROCESS"},
			{ID: "dead_task", Type: NodeTypeTask, TaskTemplateID: "DEAD"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "process"},
			{ID: "e2b", SourceID: "gw_split", TargetID: "dead_task"},
			{ID: "e3", SourceID: "process", TargetID: "gw_join"},
			// dead_task has no outgoing edges
			{ID: "e4", SourceID: "gw_join", TargetID: "end"},
		},
	}

	err := ValidateBatchGateways(def)
	s.Error(err)
	s.Contains(err.Error(), "dead-end node")
}

// --- Test 7f: Validation — interleaved batch regions fail ---

func (s *BatchGatewayTestSuite) TestBatchValidation_InterleavedBatchRegions_Fails() {
	def := WorkflowDefinition{
		ID:   "interleaved_test",
		Name: "Interleaved Batch Regions",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split1", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "gw_split2", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "task_a", Type: NodeTypeTask, TaskTemplateID: "TASK_A"},
			{ID: "gw_join1", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split1"}},
			{ID: "gw_join2", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split2"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split1"},
			{ID: "e2", SourceID: "gw_split1", TargetID: "gw_split2"},
			{ID: "e3", SourceID: "gw_split2", TargetID: "task_a"},
			{ID: "e4", SourceID: "task_a", TargetID: "gw_join1"},
			{ID: "e5", SourceID: "gw_join1", TargetID: "gw_join2"},
			{ID: "e6", SourceID: "gw_join2", TargetID: "end"},
		},
	}

	err := ValidateBatchGateways(def)
	s.Error(err)
	s.Contains(err.Error(), "outside the batch region")
}

// --- Test 7g: Validation — external edge entering batch region fails ---

func (s *BatchGatewayTestSuite) TestBatchValidation_ExternalEdgeEnteringRegion_Fails() {
	def := WorkflowDefinition{
		ID:   "external_enter_test",
		Name: "External Edge Entering Region",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "ext_task", Type: NodeTypeTask, TaskTemplateID: "EXT_TASK"},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "process", Type: NodeTypeTask, TaskTemplateID: "PROCESS"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e1b", SourceID: "start", TargetID: "ext_task"},
			{ID: "e2", SourceID: "gw_split", TargetID: "process"},
			{ID: "e_ext", SourceID: "ext_task", TargetID: "process"},
			{ID: "e3", SourceID: "process", TargetID: "gw_join"},
			{ID: "e4", SourceID: "gw_join", TargetID: "end"},
		},
	}

	err := ValidateBatchGateways(def)
	s.Error(err)
	s.Contains(err.Error(), "incoming edge from outside the batch region")
}

// --- Test 7h: Validation — external edge entering BATCH_JOIN fails ---

func (s *BatchGatewayTestSuite) TestBatchValidation_ExternalEdgeEnteringJoin_Fails() {
	def := WorkflowDefinition{
		ID:   "external_join_test",
		Name: "External Edge Entering Join",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "ext_task", Type: NodeTypeTask, TaskTemplateID: "EXT_TASK"},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "process", Type: NodeTypeTask, TaskTemplateID: "PROCESS"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e1b", SourceID: "start", TargetID: "ext_task"},
			{ID: "e2", SourceID: "gw_split", TargetID: "process"},
			{ID: "e3", SourceID: "process", TargetID: "gw_join"},
			{ID: "e_ext", SourceID: "ext_task", TargetID: "gw_join"},
			{ID: "e4", SourceID: "gw_join", TargetID: "end"},
		},
	}

	err := ValidateBatchGateways(def)
	s.Error(err)
	s.Contains(err.Error(), "outside its paired BATCH_SPLIT")
}

// --- Test 8: Depth-2 nesting (phyto consignment) ---

func (s *BatchGatewayTestSuite) TestBatchSplit_Depth2_PhytoConsignment() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	// Depth-2 nesting:
	// start → load → BATCH_SPLIT(gw_test) → (visual_inspect | lab branch) → BATCH_JOIN(join_test) → issue_cert → end
	// Lab branch: lab_sample → BATCH_SPLIT(gw_result) → (pass_through | supervisor_review) → BATCH_JOIN(join_result) → ...
	//
	// The lab_sample activity uses output_mapping to write labResult back onto each item
	// in _items. Since the child workflow runs the same interpreter, the task handler receives
	// the whole workflow variables map, and output_mapping writes the returned keys back.
	// We simulate this by having the activity return a new _items slice with labResult set.
	def := WorkflowDefinition{
		ID:   "phyto_consignment",
		Name: "Phyto Consignment",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "load", Type: NodeTypeTask, TaskTemplateID: "LOAD_CONSIGNMENT",
				OutputMapping: map[string]string{"items": "_items"}},

			// Depth-1: split by test type
			{ID: "gw_test", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "visual_inspect", Type: NodeTypeTask, TaskTemplateID: "VISUAL_INSPECTION"},
			// lab_sample writes labResult onto items via output_mapping
			{ID: "lab_sample", Type: NodeTypeTask, TaskTemplateID: "LAB_SAMPLING",
				OutputMapping: map[string]string{"tested_items": "_items"}},

			// Depth-2 (inside lab branch): split by lab result
			{ID: "gw_result", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "pass_through", Type: NodeTypeTask, TaskTemplateID: "PASS_THROUGH"},
			{ID: "supervisor_review", Type: NodeTypeTask, TaskTemplateID: "SUPERVISOR_REVIEW",
				OutputMapping: map[string]string{"reviewed_items": "_items"}},
			{ID: "join_result", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_result"}},

			{ID: "join_test", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_test"}},
			{ID: "issue_cert", Type: NodeTypeTask, TaskTemplateID: "ISSUE_CERTIFICATE"},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "load"},
			{ID: "e2", SourceID: "load", TargetID: "gw_test"},

			// Depth-1 edges
			{ID: "e3", SourceID: "gw_test", TargetID: "visual_inspect", Condition: `item.requiresLabTest != true`},
			{ID: "e4", SourceID: "gw_test", TargetID: "lab_sample", Condition: `item.requiresLabTest == true`},
			{ID: "e5", SourceID: "visual_inspect", TargetID: "join_test"},

			// Lab branch → depth-2 split
			{ID: "e6", SourceID: "lab_sample", TargetID: "gw_result"},
			{ID: "e7", SourceID: "gw_result", TargetID: "supervisor_review", Condition: `item.labResult == "fail"`},
			{ID: "e8", SourceID: "gw_result", TargetID: "pass_through", Condition: `item.labResult == "pass"`},
			{ID: "e9", SourceID: "supervisor_review", TargetID: "join_result"},
			{ID: "e10", SourceID: "pass_through", TargetID: "join_result"},
			{ID: "e11", SourceID: "join_result", TargetID: "join_test"},

			{ID: "e12", SourceID: "join_test", TargetID: "issue_cert"},
			{ID: "e13", SourceID: "issue_cert", TargetID: "end"},
		},
	}

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found in host registry", workflowID)
			}
			return nil
		})

	// load_consignment: 5 items — 3 visual, 2 lab.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "LOAD_CONSIGNMENT", mock.Anything).
		Return(map[string]any{
			"items": []any{
				map[string]any{"id": "item-1", "requiresLabTest": false, "type": "visual"},
				map[string]any{"id": "item-2", "requiresLabTest": true, "type": "lab"},
				map[string]any{"id": "item-3", "requiresLabTest": false, "type": "visual"},
				map[string]any{"id": "item-4", "requiresLabTest": true, "type": "lab"},
				map[string]any{"id": "item-5", "requiresLabTest": false, "type": "visual"},
			},
		}, nil).Once()

	// visual_inspection: runs for the 3 visual items.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "VISUAL_INSPECTION", mock.Anything).
		Return(map[string]any{}, nil)

	// lab_sampling: simulates a task that tests each item and writes labResult back.
	// The activity returns "tested_items" which output_mapping writes to "_items".
	// item-2 fails, item-4 passes.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "LAB_SAMPLING", mock.Anything).
		Return(map[string]any{
			"tested_items": []any{
				map[string]any{"id": "item-2", "requiresLabTest": true, "type": "lab", "labResult": "fail"},
				map[string]any{"id": "item-4", "requiresLabTest": true, "type": "lab", "labResult": "pass"},
			},
		}, nil)

	// pass_through and supervisor_review at depth-2.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "PASS_THROUGH", mock.Anything).
		Return(map[string]any{}, nil)
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "SUPERVISOR_REVIEW", mock.Anything).
		Return(map[string]any{
			"reviewed_items": []any{
				map[string]any{"id": "item-2", "requiresLabTest": true, "type": "lab", "labResult": "fail", "supervisorVerdict": "approved_with_conditions"},
			},
		}, nil)

	// issue_certificate: final step after all items merge.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "ISSUE_CERTIFICATE", mock.Anything).
		Return(map[string]any{}, nil)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "phyto-1"})

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal(StatusCompleted, result.Status)

	// All 5 items should be present after the top-level merge.
	items, ok := result.WorkflowVariables["_items"].([]any)
	s.True(ok, "_items should be present in final workflow variables")
	s.Len(items, 5, "all 5 items should be reunified after depth-2 batch join")

	// Verify order and item mutations:
	// item-1: visual untouched
	item1 := items[0].(map[string]any)
	s.Equal("item-1", item1["id"])
	s.Equal(false, item1["requiresLabTest"])
	s.Nil(item1["labResult"])

	// item-2: lab fail + supervisor verdict
	item2 := items[1].(map[string]any)
	s.Equal("item-2", item2["id"])
	s.Equal("fail", item2["labResult"])
	s.Equal("approved_with_conditions", item2["supervisorVerdict"])

	// item-3: visual untouched
	item3 := items[2].(map[string]any)
	s.Equal("item-3", item3["id"])
	s.Nil(item3["labResult"])

	// item-4: lab pass
	item4 := items[3].(map[string]any)
	s.Equal("item-4", item4["id"])
	s.Equal("pass", item4["labResult"])
	s.Nil(item4["supervisorVerdict"])

	// item-5: visual untouched
	item5 := items[4].(map[string]any)
	s.Equal("item-5", item5["id"])
	s.Nil(item5["labResult"])
}

// --- Test 9: Edge conditions see workflow variables (not just item) ---

func (s *BatchGatewayTestSuite) TestBatchSplit_EdgeConditionSeesWorkflowVars() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "wf_vars_in_condition",
		Name: "WF Vars in Condition",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "heavy_process", Type: NodeTypeTask, TaskTemplateID: "HEAVY"},
			{ID: "light_process", Type: NodeTypeTask, TaskTemplateID: "LIGHT"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			// Uses "threshold" from workflow variables, not from item.
			{ID: "e2", SourceID: "gw_split", TargetID: "heavy_process", Condition: `item.weight > threshold`},
			{ID: "e3", SourceID: "gw_split", TargetID: "light_process"}, // default
			{ID: "e4", SourceID: "heavy_process", TargetID: "gw_join"},
			{ID: "e5", SourceID: "light_process", TargetID: "gw_join"},
			{ID: "e6", SourceID: "gw_join", TargetID: "end"},
		},
	}

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found", workflowID)
			}
			return nil
		})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "HEAVY", mock.Anything).
		Return(map[string]any{}, nil)
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "LIGHT", mock.Anything).
		Return(map[string]any{}, nil)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "wf-vars-1"})

	initialVars := map[string]any{
		"threshold": 50.0,
		"_items": []any{
			map[string]any{"id": "A", "weight": 100.0}, // > 50 → HEAVY
			map[string]any{"id": "B", "weight": 30.0},  // <= 50 → default (LIGHT)
			map[string]any{"id": "C", "weight": 75.0},  // > 50 → HEAVY
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal(StatusCompleted, result.Status)

	items, ok := result.WorkflowVariables["_items"].([]any)
	s.True(ok)
	s.Len(items, 3, "all 3 items should be present")
}

// --- Test 10: Replay Determinism ---

func (s *BatchGatewayTestSuite) TestBatchSplit_ReplayDeterminism() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "replay_batch_test",
		Name: "Replay Batch Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "process_a", Type: NodeTypeTask, TaskTemplateID: "PROCESS_A"},
			{ID: "process_b", Type: NodeTypeTask, TaskTemplateID: "PROCESS_B"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "process_a", Condition: `item.category == "A"`},
			{ID: "e3", SourceID: "gw_split", TargetID: "process_b", Condition: `item.category == "B"`},
			{ID: "e4", SourceID: "process_a", TargetID: "gw_join"},
			{ID: "e5", SourceID: "process_b", TargetID: "gw_join"},
			{ID: "e6", SourceID: "gw_join", TargetID: "end"},
		},
	}

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found", workflowID)
			}
			return nil
		})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "PROCESS_A", mock.Anything).
		Return(map[string]any{}, nil)
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "PROCESS_B", mock.Anything).
		Return(map[string]any{}, nil)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "replay-batch-1"})

	initialVars := map[string]any{
		"_items": []any{
			map[string]any{"id": "1", "category": "A"},
			map[string]any{"id": "2", "category": "B"},
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result1 WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result1))
	s.Equal(StatusCompleted, result1.Status)

	// Run second environment with identical definition and variables to verify determinism
	env2 := s.NewTestWorkflowEnvironment()
	acts2 := &Activities{}
	env2.RegisterActivityWithOptions(acts2.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env2.RegisterActivityWithOptions(acts2.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env2.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found", workflowID)
			}
			return nil
		})
	env2.OnActivity("ExecuteTaskActivity", mock.Anything, "PROCESS_A", mock.Anything).Return(map[string]any{}, nil)
	env2.OnActivity("ExecuteTaskActivity", mock.Anything, "PROCESS_B", mock.Anything).Return(map[string]any{}, nil)
	env2.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env2.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "replay-batch-2"})

	initialVars2 := map[string]any{
		"_items": []any{
			map[string]any{"id": "1", "category": "A"},
			map[string]any{"id": "2", "category": "B"},
		},
	}

	env2.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars2)
	s.True(env2.IsWorkflowCompleted())
	s.NoError(env2.GetWorkflowError())

	var result2 WorkflowInstance
	s.NoError(env2.GetWorkflowResult(&result2))
	s.Equal(StatusCompleted, result2.Status)

	// Deterministic state checks
	s.Equal(result1.AuditTrail, result2.AuditTrail, "audit trail must be deterministic across runs")
	s.Equal(len(result1.WorkflowVariables["_items"].([]any)), len(result2.WorkflowVariables["_items"].([]any)))
}

// --- Test 11: Max depth limit guardrail ---

func (s *BatchGatewayTestSuite) TestBatchSplit_MaxDepthExceeded_Fails() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "max_depth_test",
		Name: "Max Depth Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "process", Type: NodeTypeTask, TaskTemplateID: "PROCESS"},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "process"},
			{ID: "e3", SourceID: "process", TargetID: "gw_join"},
			{ID: "e4", SourceID: "gw_join", TargetID: "end"},
		},
	}

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("AdminResolutionSignal", AdminResolutionSignal{
			NodeID: "gw_split",
			Action: AdminActionAbort,
		})
	}, 0)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "max-depth-1"})

	// Scope path with 4 levels (8 segments) = depth 4 >= DefaultMaxBatchDepth (4).
	initialVars := map[string]any{
		VarScopePath: "root/gw0/e0/gw1/e1/gw2/e2/gw3/e3",
		"_items": []any{
			map[string]any{"id": "1"},
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "maximum batch nesting depth 4 exceeded")
}

// --- Test 13: Child workflow returns corrupted items variable → error ---

func (s *BatchGatewayTestSuite) TestBatchSplit_ChildReturnsInvalidItemsType_Fails() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "corrupt_child_items_test",
		Name: "Corrupt Child Items Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "corrupt_task", Type: NodeTypeTask, TaskTemplateID: "CORRUPT_TASK",
				OutputMapping: map[string]string{"bad_items": "_items"}},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "corrupt_task"},
			{ID: "e3", SourceID: "corrupt_task", TargetID: "gw_join"},
			{ID: "e4", SourceID: "gw_join", TargetID: "end"},
		},
	}

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found in host registry", workflowID)
			}
			return nil
		})

	// CORRUPT_TASK outputs a string instead of a slice for _items
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "CORRUPT_TASK", mock.Anything).
		Return(map[string]any{"bad_items": "not-a-slice"}, nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("AdminResolutionSignal", AdminResolutionSignal{
			NodeID: "gw_split",
			Action: AdminActionAbort,
		})
	}, time.Second)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "corrupt-items-1"})

	initialVars := map[string]any{
		"_items": []any{
			map[string]any{"id": "item1", "name": "foo"},
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "returned invalid items")
}

// --- Test 14: Child workflow returns item with missing ID field → error ---

func (s *BatchGatewayTestSuite) TestBatchSplit_ChildReturnsItemMissingID_Fails() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "missing_id_child_test",
		Name: "Missing ID Child Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "strip_id_task", Type: NodeTypeTask, TaskTemplateID: "STRIP_ID_TASK",
				OutputMapping: map[string]string{"items": "_items"}},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "strip_id_task"},
			{ID: "e3", SourceID: "strip_id_task", TargetID: "gw_join"},
			{ID: "e4", SourceID: "gw_join", TargetID: "end"},
		},
	}

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found in host registry", workflowID)
			}
			return nil
		})

	// STRIP_ID_TASK outputs an item without an id field
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "STRIP_ID_TASK", mock.Anything).
		Return(map[string]any{"items": []any{map[string]any{"name": "foo"}}}, nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("AdminResolutionSignal", AdminResolutionSignal{
			NodeID: "gw_split",
			Action: AdminActionAbort,
		})
	}, time.Second)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "strip-id-1"})

	initialVars := map[string]any{
		"_items": []any{
			map[string]any{"id": "item1", "name": "foo"},
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "returned item missing required ID field")
}

// --- Test 15: Child workflow returns duplicate item ID across partitions → error ---

func (s *BatchGatewayTestSuite) TestBatchSplit_ChildReturnsDuplicateItemIDAcrossPartitions_Fails() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})

	def := WorkflowDefinition{
		ID:   "dup_id_partitions_test",
		Name: "Duplicate ID Across Partitions Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{}},
			{ID: "task_a", Type: NodeTypeTask, TaskTemplateID: "TASK_A"},
			{ID: "task_b", Type: NodeTypeTask, TaskTemplateID: "TASK_B",
				OutputMapping: map[string]string{"items": "_items"}},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2", SourceID: "gw_split", TargetID: "task_a", Condition: `item.type == "a"`},
			{ID: "e3", SourceID: "gw_split", TargetID: "task_b", Condition: `item.type == "b"`},
			{ID: "e4", SourceID: "task_a", TargetID: "gw_join"},
			{ID: "e5", SourceID: "task_b", TargetID: "gw_join"},
			{ID: "e6", SourceID: "gw_join", TargetID: "end"},
		},
	}

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found in host registry", workflowID)
			}
			return nil
		})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_A", mock.Anything).
		Return(map[string]any{}, nil)

	// TASK_B returns an item with id "item-a", which already belongs to partition A
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TASK_B", mock.Anything).
		Return(map[string]any{"items": []any{map[string]any{"id": "item-a", "type": "b"}}}, nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("AdminResolutionSignal", AdminResolutionSignal{
			NodeID: "gw_split",
			Action: AdminActionAbort,
		})
	}, time.Second)

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "dup-partition-id-1"})

	initialVars := map[string]any{
		"_items": []any{
			map[string]any{"id": "item-a", "type": "a"},
			map[string]any{"id": "item-b", "type": "b"},
		},
	}

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, initialVars)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "duplicate item ID \"item-a\" returned across child partitions")
}
