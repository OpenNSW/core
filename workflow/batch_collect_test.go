// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/OpenNSW/core/shared/maputil"
)

// newCollectTestEnv registers the activities and the interpreter a batch collect test needs.
// Completion callbacks for child workflows (IDs containing "--") report "not found", as the
// host registry only knows top-level workflows.
func (s *BatchGatewayTestSuite) newCollectTestEnv() *testsuite.TestWorkflowEnvironment {
	env := s.NewTestWorkflowEnvironment()
	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any, _ int64) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found in host registry", workflowID)
			}
			return nil
		})
	return env
}

func (s *BatchGatewayTestSuite) onTask(env *testsuite.TestWorkflowEnvironment, templateID string, output map[string]any) {
	env.OnActivity("ExecuteTaskActivity", mock.Anything, templateID, mock.Anything, mock.Anything, mock.Anything).
		Return(output, nil)
}

func (s *BatchGatewayTestSuite) runCollectWorkflow(env *testsuite.TestWorkflowEnvironment, def WorkflowDefinition, vars map[string]any) map[string]any {
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: def.ID})
	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, vars)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal(StatusCompleted, result.Status)
	return result.WorkflowVariables
}

func (s *BatchGatewayTestSuite) collected(vars map[string]any, path string) []any {
	value, exists := maputil.GetNestedKey(vars, path)
	s.True(exists, "collect destination %q should be set", path)
	list, ok := value.([]any)
	s.True(ok, "collect destination %q should be a list, got %T", path, value)
	return list
}

var collectTestItems = []any{
	map[string]any{"id": "C1", "type": "food"},
	map[string]any{"id": "C2", "type": "goods"},
	map[string]any{"id": "C3", "type": "food"},
}

// twoPartitionCollectDef splits items into food and goods partitions, runs one task in each,
// and collects treatment.doc from both at the join.
func twoPartitionCollectDef(id string) WorkflowDefinition {
	return WorkflowDefinition{
		ID:   id,
		Name: id,
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{ItemsVariable: "commodities"}},
			{ID: "food_task", Type: NodeTypeTask, TaskTemplateID: "FOOD_TASK",
				OutputMapping: map[string]string{"doc?": "treatment.doc"}},
			{ID: "goods_task", Type: NodeTypeTask, TaskTemplateID: "GOODS_TASK",
				OutputMapping: map[string]string{"doc?": "treatment.doc"}},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split", ItemsVariable: "commodities",
					Collect: map[string]string{"treatment.doc": "treatment.docs"}}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_split"},
			{ID: "e2_food", SourceID: "gw_split", TargetID: "food_task", Condition: `item.type == "food"`},
			{ID: "e3_goods", SourceID: "gw_split", TargetID: "goods_task", Condition: `item.type == "goods"`},
			{ID: "e4", SourceID: "food_task", TargetID: "gw_join"},
			{ID: "e5", SourceID: "goods_task", TargetID: "gw_join"},
			{ID: "e6", SourceID: "gw_join", TargetID: "end"},
		},
	}
}

func (s *BatchGatewayTestSuite) TestBatchJoinCollect_GathersEachPartitionsValue() {
	env := s.newCollectTestEnv()
	s.onTask(env, "FOOD_TASK", map[string]any{"doc": "food-cert"})
	s.onTask(env, "GOODS_TASK", map[string]any{"doc": "goods-cert"})

	vars := s.runCollectWorkflow(env, twoPartitionCollectDef("collect-two-partitions"),
		map[string]any{"commodities": collectTestItems})

	// Partition order follows the sorted edge IDs: e2_food before e3_goods.
	s.Equal([]any{"food-cert", "goods-cert"}, s.collected(vars, "treatment.docs"))
	s.Len(vars["commodities"], 3, "items still merge back alongside the collected variable")
}

func (s *BatchGatewayTestSuite) TestBatchJoinCollect_SkipsValueTheChildOnlyInherited() {
	env := s.newCollectTestEnv()
	s.onTask(env, "FOOD_TASK", map[string]any{"doc": "food-cert"})
	s.onTask(env, "GOODS_TASK", map[string]any{})

	vars := s.runCollectWorkflow(env, twoPartitionCollectDef("collect-skips-inherited"), map[string]any{
		"commodities": collectTestItems,
		"treatment":   map[string]any{"doc": "set-before-the-split"},
	})

	// The goods child never set treatment.doc, so its inherited copy is not collected.
	s.Equal([]any{"food-cert"}, s.collected(vars, "treatment.docs"))
}

func (s *BatchGatewayTestSuite) TestBatchJoinCollect_FlattensWhatANestedJoinCollected() {
	env := s.newCollectTestEnv()
	s.onTask(env, "PROVIDER_A_TASK", map[string]any{"doc": "a-cert"})
	s.onTask(env, "PROVIDER_B_TASK", map[string]any{"doc": "b-cert"})

	// Outer split by type; food items go through an inner split by provider, each provider
	// setting treatment.doc, which the inner join collects into treatment.docs. Goods items
	// bypass. The outer join collects treatment.docs again and should get one flat list.
	def := WorkflowDefinition{
		ID:   "collect-nested",
		Name: "collect-nested",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_outer", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{ItemsVariable: "commodities"}},
			{ID: "gw_inner", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{ItemsVariable: "commodities"}},
			{ID: "provider_a", Type: NodeTypeTask, TaskTemplateID: "PROVIDER_A_TASK",
				OutputMapping: map[string]string{"doc": "treatment.doc"}},
			{ID: "provider_b", Type: NodeTypeTask, TaskTemplateID: "PROVIDER_B_TASK",
				OutputMapping: map[string]string{"doc": "treatment.doc"}},
			{ID: "gw_inner_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_inner", ItemsVariable: "commodities",
					Collect: map[string]string{"treatment.doc": "treatment.docs"}}},
			{ID: "gw_outer_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_outer", ItemsVariable: "commodities",
					Collect: map[string]string{"treatment.docs": "treatment.docs"}}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "gw_outer"},
			{ID: "e2_food", SourceID: "gw_outer", TargetID: "gw_inner", Condition: `item.type == "food"`},
			{ID: "e3_goods", SourceID: "gw_outer", TargetID: "gw_outer_join", Condition: `item.type == "goods"`},
			{ID: "e4_a", SourceID: "gw_inner", TargetID: "provider_a", Condition: `item.id == "C1"`},
			{ID: "e5_b", SourceID: "gw_inner", TargetID: "provider_b", Condition: `item.id == "C3"`},
			{ID: "e6", SourceID: "provider_a", TargetID: "gw_inner_join"},
			{ID: "e7", SourceID: "provider_b", TargetID: "gw_inner_join"},
			{ID: "e8", SourceID: "gw_inner_join", TargetID: "gw_outer_join"},
			{ID: "e9", SourceID: "gw_outer_join", TargetID: "end"},
		},
	}

	vars := s.runCollectWorkflow(env, def, map[string]any{"commodities": collectTestItems})

	s.Equal([]any{"a-cert", "b-cert"}, s.collected(vars, "treatment.docs"))
}

func (s *BatchGatewayTestSuite) TestBatchJoinCollect_EmptyItemsSetsEmptyList() {
	env := s.newCollectTestEnv()

	vars := s.runCollectWorkflow(env, twoPartitionCollectDef("collect-empty-items"),
		map[string]any{"commodities": []any{}})

	s.Empty(s.collected(vars, "treatment.docs"))
}

func (s *BatchGatewayTestSuite) TestBatchValidation_CollectEntries() {
	cases := []struct {
		name    string
		collect map[string]string
		wantErr string
	}{
		{"empty source", map[string]string{"": "treatment.docs"}, "need both a source and a destination"},
		{"empty destination", map[string]string{"treatment.doc": ""}, "need both a source and a destination"},
		{"writes the items variable", map[string]string{"treatment.doc": "commodities"}, "cannot read or write the items variable"},
		{"reads the items variable", map[string]string{"commodities": "treatment.items"}, "cannot read or write the items variable"},
		{"two sources, one destination", map[string]string{"a.doc": "treatment.docs", "b.doc": "treatment.docs"}, `writes "treatment.docs" from both`},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			def := twoPartitionCollectDef("collect-validation")
			for i := range def.Nodes {
				if def.Nodes[i].ID == "gw_join" {
					def.Nodes[i].BatchJoin.Collect = tc.collect
				}
			}
			err := ValidateBatchGateways(def)
			s.Error(err)
			s.Contains(err.Error(), tc.wantErr)
		})
	}
	s.NoError(ValidateBatchGateways(twoPartitionCollectDef("collect-valid")))
}
