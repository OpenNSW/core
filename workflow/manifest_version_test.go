// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// batchManifestWorkflow: start -> load_items -> BATCH_SPLIT -> (food | goods) -> BATCH_JOIN -> end.
func batchManifestWorkflow() WorkflowDefinition {
	return WorkflowDefinition{
		ID: "batch_manifest_test",
		Nodes: []Node{
			{ID: "start", Type: NodeTypeStart},
			{ID: "load_items", Type: NodeTypeTask, TaskTemplateID: "LOAD_ITEMS",
				OutputMapping: map[string]string{"items": "commodities"}},
			{ID: "gw", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchSplit,
				BatchGateway: &BatchGatewayConfig{ItemsVariable: "commodities", IDField: "id"}},
			{ID: "food", Type: NodeTypeTask, TaskTemplateID: "FOOD_INSPECTION"},
			{ID: "goods", Type: NodeTypeTask, TaskTemplateID: "GOODS_INSPECTION"},
			{ID: "join", Type: NodeTypeGateway, GatewayType: GatewayTypeBatchJoin,
				BatchJoin: &BatchJoinConfig{GatewayNodeID: "gw", ItemsVariable: "commodities", IDField: "id"}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "e1", SourceID: "start", TargetID: "load_items"},
			{ID: "e2", SourceID: "load_items", TargetID: "gw"},
			{ID: "e3", SourceID: "gw", TargetID: "food", Condition: `item.type == "food"`},
			{ID: "e4", SourceID: "gw", TargetID: "goods", Condition: `item.type == "goods"`},
			{ID: "e5", SourceID: "food", TargetID: "join"},
			{ID: "e6", SourceID: "goods", TargetID: "join"},
			{ID: "e7", SourceID: "join", TargetID: "end"},
		},
	}
}

// runRecordingActivations runs def with vars and returns the manifest version of every task
// activation, keyed by task template ID.
func runRecordingActivations(t *testing.T, def WorkflowDefinition, vars map[string]any) map[string][]int {
	t.Helper()
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.AdminParkActivity, activity.RegisterOptions{Name: "AdminParkActivity"})
	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})
	mockWorkflowCompletedIgnoringChildren(env)

	var mu sync.Mutex
	seen := make(map[string][]int)
	env.OnActivity("ExecuteTaskActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, templateID string, inputs map[string]any, _ string, ref ActivationRef) (TaskResult, error) {
			mu.Lock()
			seen[templateID] = append(seen[templateID], ref.ManifestVersion)
			mu.Unlock()
			if templateID == "LOAD_ITEMS" {
				return TaskResult{Output: map[string]any{"items": []any{
					map[string]any{"id": "i1", "type": "food"},
					map[string]any{"id": "i2", "type": "goods"},
				}}}, nil
			}
			if c, ok := inputs["commodities"]; ok {
				return TaskResult{Output: map[string]any{"commodities": c}}, nil
			}
			return TaskResult{Output: map[string]any{}}, nil
		})

	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "manifest-test"})
	env.ExecuteWorkflow(GraphInterpreterWorkflow, def, vars)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	return seen
}

// Every task activation, in the top-level workflow and in every child a gateway starts, carries
// the manifest version the host started the workflow with.
func TestManifestVersionReachesEveryTaskActivation(t *testing.T) {
	for name, def := range map[string]WorkflowDefinition{
		"BATCH_SPLIT":    batchManifestWorkflow(),
		"PARALLEL_SPLIT": buildParallelMergeWorkflow(map[string]string{"commodities": "id"}),
	} {
		t.Run(name, func(t *testing.T) {
			seen := runRecordingActivations(t, def, map[string]any{VarManifestVersion: 3})
			require.NotEmpty(t, seen)
			children := 0
			for template, versions := range seen {
				for _, v := range versions {
					require.Equal(t, 3, v, "activation of %s", template)
				}
				if template != "LOAD_ITEMS" {
					children += len(versions)
				}
			}
			require.Equal(t, 2, children, "both child branches should have run a task")
		})
	}

	t.Run("a workflow started without a version reports 0", func(t *testing.T) {
		seen := runRecordingActivations(t, batchManifestWorkflow(), map[string]any{})
		for template, versions := range seen {
			for _, v := range versions {
				require.Equal(t, 0, v, "activation of %s", template)
			}
		}
	})
}

func TestManifestVersionValueForms(t *testing.T) {
	for name, tc := range map[string]struct {
		value any
		want  int
	}{
		"int":                  {4, 4},
		"int64":                {int64(4), 4},
		"float64 from JSON":    {float64(4), 4},
		"json.Number":          {json.Number("4"), 4},
		"absent":               {nil, 0},
		"zero":                 {0, 0},
		"negative int":         {-2, 0},
		"negative float":       {float64(-2), 0},
		"fractional":           {4.5, 0},
		"string":               {"4", 0},
		"non-integer Number":   {json.Number("4.5"), 0},
		"negative json.Number": {json.Number("-1"), 0},
	} {
		t.Run(name, func(t *testing.T) {
			vars := map[string]any{}
			if tc.value != nil {
				vars[VarManifestVersion] = tc.value
			}
			g := &graphInterpreter{instance: &WorkflowInstance{WorkflowVariables: vars}}
			require.Equal(t, tc.want, g.manifestVersion())
		})
	}
}
