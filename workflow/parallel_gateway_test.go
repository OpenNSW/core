// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type ParallelGatewayTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
}

func TestParallelGatewayTestSuite(t *testing.T) {
	suite.Run(t, new(ParallelGatewayTestSuite))
}

// buildParallelMergeWorkflow: start -> load_items -> PARALLEL_SPLIT -> (lab_task | visual_task)
// -> PARALLEL_JOIN -> end. Both branches read and independently annotate the same shared
// `commodities` array — the scenario that used to clobber before this fix.
func buildParallelMergeWorkflow(mergeByID map[string]string) WorkflowDefinition {
	return WorkflowDefinition{
		ID:   "parallel_merge_test",
		Name: "Parallel Merge Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "load_items", Type: NodeTypeTask, TaskTemplateID: "LOAD_ITEMS",
				OutputMapping: map[string]string{"items": "commodities"}},
			{ID: "psplit", Type: NodeTypeGateway, GatewayType: GatewayTypeParallelSplit},
			{ID: "lab_task", Type: NodeTypeTask, TaskTemplateID: "LAB_TASK",
				InputMapping:  map[string]string{"commodities": "commodities"},
				OutputMapping: map[string]string{"commodities": "commodities"}},
			{ID: "visual_task", Type: NodeTypeTask, TaskTemplateID: "VISUAL_TASK",
				InputMapping:  map[string]string{"commodities": "commodities"},
				OutputMapping: map[string]string{"commodities": "commodities"}},
			{ID: "pjoin", Type: NodeTypeGateway, GatewayType: GatewayTypeParallelJoin,
				ParallelJoin: &ParallelJoinConfig{GatewayNodeID: "psplit", MergeByID: mergeByID}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "load_items"},
			{ID: "e2", SourceID: "load_items", TargetID: "psplit"},
			{ID: "e3", SourceID: "psplit", TargetID: "lab_task"},
			{ID: "e4", SourceID: "psplit", TargetID: "visual_task"},
			{ID: "e5", SourceID: "lab_task", TargetID: "pjoin"},
			{ID: "e6", SourceID: "visual_task", TargetID: "pjoin"},
			{ID: "e7", SourceID: "pjoin", TargetID: "end"},
		},
	}
}

func mockWorkflowCompletedIgnoringChildren(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, workflowID string, _ map[string]any) error {
			if strings.Contains(workflowID, "--") {
				return fmt.Errorf("workflow %s not found in host registry", workflowID)
			}
			return nil
		})
}

// TestParallelSplit_ItemFieldMerge_BothBranchesFieldsSurvive is the direct regression test for
// the bug this fix addresses: before it, whichever branch's BATCH_SPLIT-style write-back ran
// last would silently revert the other branch's contribution to the shared `commodities`
// array. Here lab_task adds lab_result to every item and visual_task adds visual_result to
// every item (both branches actively touch the SAME items, not disjoint ones) — both fields
// must be present on both items afterward, regardless of which branch's child workflow happens
// to finish first.
func (s *ParallelGatewayTestSuite) TestParallelSplit_ItemFieldMerge_BothBranchesFieldsSurvive() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	mockWorkflowCompletedIgnoringChildren(env)

	def := buildParallelMergeWorkflow(map[string]string{"commodities": "id"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "LOAD_ITEMS", mock.Anything).
		Return(map[string]any{
			"items": []any{
				map[string]any{"id": "item-1", "name": "Cut Flowers"},
				map[string]any{"id": "item-2", "name": "Timber"},
			},
		}, nil).Once()

	// lab_task: annotates every item with lab_result, echoing the identity fields it saw.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "LAB_TASK", mock.Anything).
		Return(func(_ context.Context, _ string, inputs map[string]any) (map[string]any, error) {
			in, _ := inputs["commodities"].([]any)
			out := make([]any, 0, len(in))
			for _, raw := range in {
				item, _ := raw.(map[string]any)
				updated := map[string]any{"id": item["id"], "name": item["name"], "lab_result": "pass"}
				out = append(out, updated)
			}
			return map[string]any{"commodities": out}, nil
		}).Once()

	// visual_task: annotates every item with visual_result, independently of lab_task.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "VISUAL_TASK", mock.Anything).
		Return(func(_ context.Context, _ string, inputs map[string]any) (map[string]any, error) {
			in, _ := inputs["commodities"].([]any)
			out := make([]any, 0, len(in))
			for _, raw := range in {
				item, _ := raw.(map[string]any)
				updated := map[string]any{"id": item["id"], "name": item["name"], "visual_result": "clean"}
				out = append(out, updated)
			}
			return map[string]any{"commodities": out}, nil
		}).Once()

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "parallel-merge-test-1"})

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result))

	items, ok := result.WorkflowVariables["commodities"].([]any)
	s.Require().True(ok, "commodities should be a []any after merge")
	s.Len(items, 2)

	byID := make(map[string]map[string]any, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		byID[item["id"].(string)] = item
	}

	s.Equal("pass", byID["item-1"]["lab_result"], "item-1 must keep lab's contribution")
	s.Equal("clean", byID["item-1"]["visual_result"], "item-1 must ALSO keep visual's contribution — this is the fix")
	s.Equal("pass", byID["item-2"]["lab_result"], "item-2 must keep lab's contribution")
	s.Equal("clean", byID["item-2"]["visual_result"], "item-2 must ALSO keep visual's contribution — this is the fix")
}

// TestParallelSplit_GenericMapMerge_DisjointFieldsSurvive covers a shared variable that is a
// map (not an array), with each branch writing a different sub-field — the FCAU-style pattern.
// This already worked before the fix (SetNestedKey's own map-merge case), and must keep
// working now that branches are isolated child workflows instead of in-process coroutines.
func (s *ParallelGatewayTestSuite) TestParallelSplit_GenericMapMerge_DisjointFieldsSurvive() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	mockWorkflowCompletedIgnoringChildren(env)

	def := WorkflowDefinition{
		ID:   "parallel_map_merge_test",
		Name: "Parallel Map Merge Test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "psplit", Type: NodeTypeGateway, GatewayType: GatewayTypeParallelSplit},
			{ID: "track_a", Type: NodeTypeTask, TaskTemplateID: "TRACK_A",
				OutputMapping: map[string]string{"value": "status.a_done"}},
			{ID: "track_b", Type: NodeTypeTask, TaskTemplateID: "TRACK_B",
				OutputMapping: map[string]string{"value": "status.b_done"}},
			{ID: "pjoin", Type: NodeTypeGateway, GatewayType: GatewayTypeParallelJoin,
				ParallelJoin: &ParallelJoinConfig{GatewayNodeID: "psplit"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "psplit"},
			{ID: "e2", SourceID: "psplit", TargetID: "track_a"},
			{ID: "e3", SourceID: "psplit", TargetID: "track_b"},
			{ID: "e4", SourceID: "track_a", TargetID: "pjoin"},
			{ID: "e5", SourceID: "track_b", TargetID: "pjoin"},
			{ID: "e6", SourceID: "pjoin", TargetID: "end"},
		},
	}

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TRACK_A", mock.Anything).
		Return(map[string]any{"value": true}, nil).Once()
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "TRACK_B", mock.Anything).
		Return(map[string]any{"value": true}, nil).Once()

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "parallel-map-merge-test-1"})

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result))

	status, ok := result.WorkflowVariables["status"].(map[string]any)
	s.Require().True(ok, "status should be a map after merge")
	s.Equal(true, status["a_done"], "track_a's field must survive")
	s.Equal(true, status["b_done"], "track_b's field must ALSO survive")
}

// TestParallelSplit_SameFieldConflict_LastBranchWinsDeterministically documents the one case
// the merge cannot resolve on its own: two branches writing DIFFERENT values to the exact same
// field of the exact same item. There is no data-driven "correct" answer here — this just
// pins down that the outcome is deterministic (branches merge in sorted edge-ID order, last one
// wins for a genuinely conflicting field) rather than flaky, so a workflow author who hits this
// knows to give each branch its own field name instead of relying on the resolution order.
func (s *ParallelGatewayTestSuite) TestParallelSplit_SameFieldConflict_LastBranchWinsDeterministically() {
	env := s.NewTestWorkflowEnvironment()

	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	mockWorkflowCompletedIgnoringChildren(env)

	def := buildParallelMergeWorkflow(map[string]string{"commodities": "id"})

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "LOAD_ITEMS", mock.Anything).
		Return(map[string]any{
			"items": []any{map[string]any{"id": "item-1"}},
		}, nil).Once()

	// Both branches write the SAME field ("failure_reason") with DIFFERENT values.
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "LAB_TASK", mock.Anything).
		Return(map[string]any{
			"commodities": []any{map[string]any{"id": "item-1", "failure_reason": "lab reason"}},
		}, nil).Once()
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "VISUAL_TASK", mock.Anything).
		Return(map[string]any{
			"commodities": []any{map[string]any{"id": "item-1", "failure_reason": "visual reason"}},
		}, nil).Once()

	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "parallel-conflict-test-1"})

	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, map[string]any{})

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result WorkflowInstance
	s.NoError(env.GetWorkflowResult(&result))

	items, ok := result.WorkflowVariables["commodities"].([]any)
	s.Require().True(ok)
	s.Require().Len(items, 1)
	item, _ := items[0].(map[string]any)

	// psplit's outgoing edges are e3 (-> lab_task) and e4 (-> visual_task); sorted by edge ID,
	// e3 is applied before e4, so visual_task's (later) value wins the conflicting field.
	s.Equal("visual reason", item["failure_reason"],
		"a genuinely conflicting field resolves via the fixed, documented branch order — not a merge algorithm decision")
}
