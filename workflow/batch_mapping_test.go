// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/OpenNSW/core/shared/maputil"
)

// newBatchMappingTestEnv registers the activities and the interpreter a batch mapping test
// needs. Completion callbacks for child workflows (IDs containing "--") report "not found",
// as the host registry only knows top-level workflows.
func (s *BatchGatewayTestSuite) newBatchMappingTestEnv() *testsuite.TestWorkflowEnvironment {
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

func (s *BatchGatewayTestSuite) runBatchMappingWorkflow(env *testsuite.TestWorkflowEnvironment, def WorkflowDefinition, vars map[string]any) map[string]any {
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: def.ID})
	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, vars)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal(StatusCompleted, result.Status)
	return result.WorkflowVariables
}

func (s *BatchGatewayTestSuite) listAt(vars map[string]any, path string) []any {
	value, exists := maputil.GetNestedKey(vars, path)
	s.True(exists, "output destination %q should be set", path)
	list, ok := value.([]any)
	s.True(ok, "output destination %q should be a list, got %T", path, value)
	return list
}

var batchMappingTestItems = []any{
	map[string]any{"id": "C1", "type": "food"},
	map[string]any{"id": "C2", "type": "goods"},
	map[string]any{"id": "C3", "type": "food"},
}

// twoPartitionDef splits items into food and goods partitions and runs one task in each. Each
// task may set treatment.doc; the split's output_mapping brings it out as treatment.docs.
func twoPartitionDef(id string) WorkflowDefinition {
	return WorkflowDefinition{
		ID:   id,
		Name: id,
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_split", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway:  &BatchGatewayConfig{ItemsVariable: "commodities"},
				OutputMapping: map[string]string{"treatment.doc": "treatment.docs"}},
			{ID: "food_task", Type: NodeTypeTask, TaskTemplateID: "FOOD_TASK",
				OutputMapping: map[string]string{"doc?": "treatment.doc"}},
			{ID: "goods_task", Type: NodeTypeTask, TaskTemplateID: "GOODS_TASK",
				OutputMapping: map[string]string{"doc?": "treatment.doc"}},
			{ID: "gw_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_split", ItemsVariable: "commodities"}},
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

func splitNode(def *WorkflowDefinition) *Node {
	for i := range def.Nodes {
		if def.Nodes[i].ID == "gw_split" {
			return &def.Nodes[i]
		}
	}
	return nil
}

// --- output_mapping ---

func (s *BatchGatewayTestSuite) TestBatchSplitOutputMapping_GathersEachPartitionsValue() {
	env := s.newBatchMappingTestEnv()
	s.onTask(env, "FOOD_TASK", map[string]any{"doc": "food-cert"})
	s.onTask(env, "GOODS_TASK", map[string]any{"doc": "goods-cert"})

	vars := s.runBatchMappingWorkflow(env, twoPartitionDef("output-two-partitions"),
		map[string]any{"commodities": batchMappingTestItems})

	// Partition order follows the sorted edge IDs: e2_food before e3_goods.
	s.Equal([]any{"food-cert", "goods-cert"}, s.listAt(vars, "treatment.docs"))
	s.Len(vars["commodities"], 3, "items still merge back alongside the output")
}

func (s *BatchGatewayTestSuite) TestBatchSplitOutputMapping_KeepsOneOfEachValue() {
	env := s.newBatchMappingTestEnv()
	shared := map[string]any{"name": "fumigation", "url": "certs/1.pdf"}
	s.onTask(env, "FOOD_TASK", map[string]any{"doc": shared})
	s.onTask(env, "GOODS_TASK", map[string]any{"doc": map[string]any{"url": "certs/1.pdf", "name": "fumigation"}})

	vars := s.runBatchMappingWorkflow(env, twoPartitionDef("output-unique"),
		map[string]any{"commodities": batchMappingTestItems})

	s.Equal([]any{shared}, s.listAt(vars, "treatment.docs"))
}

func (s *BatchGatewayTestSuite) TestBatchSplitOutputMapping_PartitionWithoutTheValueContributesNothing() {
	env := s.newBatchMappingTestEnv()
	s.onTask(env, "FOOD_TASK", map[string]any{"doc": "food-cert"})
	s.onTask(env, "GOODS_TASK", map[string]any{})

	vars := s.runBatchMappingWorkflow(env, twoPartitionDef("output-missing-in-one"),
		map[string]any{"commodities": batchMappingTestItems})

	s.Equal([]any{"food-cert"}, s.listAt(vars, "treatment.docs"))
}

func (s *BatchGatewayTestSuite) TestBatchSplitOutputMapping_FlattensWhatANestedSplitGathered() {
	env := s.newBatchMappingTestEnv()
	s.onTask(env, "PROVIDER_A_TASK", map[string]any{"doc": "a-cert"})
	s.onTask(env, "PROVIDER_B_TASK", map[string]any{"doc": "b-cert"})

	// Outer split by type; food items go through an inner split by item, each inner partition
	// setting treatment.doc, which the inner split gathers into treatment.docs. Goods items
	// bypass. The outer split gathers treatment.docs again and should get one flat list.
	def := WorkflowDefinition{
		ID:   "output-nested",
		Name: "output-nested",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "gw_outer", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway:  &BatchGatewayConfig{ItemsVariable: "commodities"},
				OutputMapping: map[string]string{"treatment.docs": "treatment.docs"}},
			{ID: "gw_inner", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway:  &BatchGatewayConfig{ItemsVariable: "commodities"},
				OutputMapping: map[string]string{"treatment.doc": "treatment.docs"}},
			{ID: "provider_a", Type: NodeTypeTask, TaskTemplateID: "PROVIDER_A_TASK",
				OutputMapping: map[string]string{"doc": "treatment.doc"}},
			{ID: "provider_b", Type: NodeTypeTask, TaskTemplateID: "PROVIDER_B_TASK",
				OutputMapping: map[string]string{"doc": "treatment.doc"}},
			{ID: "gw_inner_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_inner", ItemsVariable: "commodities"}},
			{ID: "gw_outer_join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw_outer", ItemsVariable: "commodities"}},
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

	vars := s.runBatchMappingWorkflow(env, def, map[string]any{"commodities": batchMappingTestItems})

	s.Equal([]any{"a-cert", "b-cert"}, s.listAt(vars, "treatment.docs"))
}

func (s *BatchGatewayTestSuite) TestBatchSplitOutputMapping_EmptyItemsSetsEmptyList() {
	env := s.newBatchMappingTestEnv()

	vars := s.runBatchMappingWorkflow(env, twoPartitionDef("output-empty-items"),
		map[string]any{"commodities": []any{}})

	s.Empty(s.listAt(vars, "treatment.docs"))
}

// --- input_mapping ---

// recordTaskInputs makes FOOD_TASK and GOODS_TASK record the inputs they were given.
func (s *BatchGatewayTestSuite) recordTaskInputs(env *testsuite.TestWorkflowEnvironment) func() []map[string]any {
	var mu sync.Mutex
	var seen []map[string]any
	for _, tmpl := range []string{"FOOD_TASK", "GOODS_TASK"} {
		env.OnActivity("ExecuteTaskActivity", mock.Anything, tmpl, mock.Anything, mock.Anything, mock.Anything).Return(
			func(_ context.Context, _ string, inputs map[string]any, _ string, _ ActivationRef) (map[string]any, error) {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, inputs)
				return map[string]any{}, nil
			})
	}
	return func() []map[string]any { mu.Lock(); defer mu.Unlock(); return seen }
}

// withTaskInputs makes both partition tasks read every variable the tests look for, optionally.
func withTaskInputs(def WorkflowDefinition) WorkflowDefinition {
	inputs := map[string]string{
		"ref?":                   "ref",
		"npqs.reference_number?": "parent_ref",
		"secret?":                "secret",
		"commodities?":           "items",
		"_root_workflow_id?":     "root",
	}
	for i := range def.Nodes {
		if def.Nodes[i].Type == NodeTypeTask {
			def.Nodes[i].InputMapping = inputs
		}
	}
	return def
}

var inputMappingParentVars = map[string]any{
	"commodities": batchMappingTestItems,
	"npqs":        map[string]any{"reference_number": "NPQS-1"},
	"secret":      "not-for-partitions",
}

func (s *BatchGatewayTestSuite) TestBatchSplitInputMapping_PartitionsSeeOnlyMappedAndEngineVariables() {
	env := s.newBatchMappingTestEnv()
	inputs := s.recordTaskInputs(env)
	def := withTaskInputs(twoPartitionDef("input-mapped"))
	splitNode(&def).InputMapping = map[string]string{"npqs.reference_number": "ref"}

	s.runBatchMappingWorkflow(env, def, inputMappingParentVars)

	s.Len(inputs(), 2, "one task per partition")
	for _, in := range inputs() {
		s.Equal("NPQS-1", in["ref"], "mapped variable reaches the partition")
		s.NotContains(in, "parent_ref", "the source path itself is not copied")
		s.NotContains(in, "secret", "unmapped variables stay in the parent")
		s.NotEmpty(in["items"], "the partition's items are always set")
		s.Equal("input-mapped", in["root"], "engine context variables pass through")
	}
}

func (s *BatchGatewayTestSuite) TestBatchSplitInputMapping_WithoutOnePartitionsSeeOnlyTheirItems() {
	env := s.newBatchMappingTestEnv()
	inputs := s.recordTaskInputs(env)

	s.runBatchMappingWorkflow(env, withTaskInputs(twoPartitionDef("input-unmapped")), inputMappingParentVars)

	s.Len(inputs(), 2)
	for _, in := range inputs() {
		s.NotContains(in, "parent_ref")
		s.NotContains(in, "secret")
		s.NotEmpty(in["items"])
		s.Equal("input-unmapped", in["root"], "engine context variables pass through")
	}
}

func (s *BatchGatewayTestSuite) TestBatchSplitInputMapping_MissingRequiredSourceParks() {
	env := s.newBatchMappingTestEnv()
	def := twoPartitionDef("input-missing")
	splitNode(&def).InputMapping = map[string]string{"npqs.permit_number": "permit", "npqs.note?": "note"}

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("AdminResolutionSignal", AdminResolutionSignal{
			NodeID:       "gw_split",
			ActivationID: parkedActivationID(s.T(), env, "gw_split"),
			Action:       AdminActionAbort,
		})
	}, time.Millisecond)
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: def.ID})
	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, inputMappingParentVars)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "npqs.permit_number")
	s.NotContains(err.Error(), "npqs.note", "an optional source may be missing")
}

// --- validation ---

func (s *BatchGatewayTestSuite) TestBatchValidation_SplitMappings() {
	cases := []struct {
		name    string
		input   map[string]string
		output  map[string]string
		wantErr string
	}{
		{"output: empty source", nil, map[string]string{"": "treatment.docs"}, "need both a source and a destination"},
		{"output: empty destination", nil, map[string]string{"treatment.doc": ""}, "need both a source and a destination"},
		{"output: writes the items variable", nil, map[string]string{"treatment.doc": "commodities"}, "cannot read or write the items variable"},
		{"output: reads the items variable", nil, map[string]string{"commodities": "treatment.items"}, "cannot read or write the items variable"},
		{"output: two sources, one destination", nil, map[string]string{"a.doc": "treatment.docs", "b.doc": "treatment.docs"}, `writes "treatment.docs" from both`},
		{"input: empty destination", map[string]string{"npqs.reference_number": ""}, nil, "need both a source and a destination"},
		{"input: writes the items variable", map[string]string{"npqs.items": "commodities"}, nil, "cannot write the items variable"},
		{"input: writes an engine variable", map[string]string{"npqs.root": "_root_workflow_id"}, nil, "reserved for the engine"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			def := twoPartitionDef("mapping-validation")
			splitNode(&def).InputMapping = tc.input
			splitNode(&def).OutputMapping = tc.output
			err := ValidateBatchGateways(def)
			s.Error(err)
			s.Contains(err.Error(), tc.wantErr)
		})
	}

	s.Run("mappings on the join", func() {
		def := twoPartitionDef("mapping-on-join")
		for i := range def.Nodes {
			if def.Nodes[i].ID == "gw_join" {
				def.Nodes[i].OutputMapping = map[string]string{"treatment.doc": "treatment.docs"}
			}
		}
		err := ValidateBatchGateways(def)
		s.Error(err)
		s.Contains(err.Error(), "belong on the paired BATCH_SPLIT")
	})

	s.NoError(ValidateBatchGateways(twoPartitionDef("mapping-valid")))
}
