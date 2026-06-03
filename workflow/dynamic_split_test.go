package engine

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type NSWEngineTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
}

func TestNSWEngineTestSuite(t *testing.T) {
	suite.Run(t, new(NSWEngineTestSuite))
}

func (s *NSWEngineTestSuite) TestDynamicFanOutWithDifferentTemplates() {
	env := s.NewTestWorkflowEnvironment()

	// Register activities
	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.FetchWorkflowDefinitionActivity, activity.RegisterOptions{Name: "FetchWorkflowDefinitionActivity"})

	// 1. Define the first child workflow definition (Phyto)
	phytoDef := WorkflowDefinition{
		ID: "oga_phyto_workflow",
		Nodes: []Node{
			{ID: "p_start", Type: NodeTypeStart},
			{
				ID:             "p_inspect",
				Type:           NodeTypeTask,
				TaskTemplateID: "run_phyto_inspection",
				OutputMapping:  map[string]string{"inspection_status": "phyto_status"},
			},
			{ID: "p_end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "pe1", SourceID: "p_start", TargetID: "p_inspect"},
			{ID: "pe2", SourceID: "p_inspect", TargetID: "p_end"},
		},
	}

	// 2. Define the second child workflow definition (Health)
	healthDef := WorkflowDefinition{
		ID: "oga_health_workflow",
		Nodes: []Node{
			{ID: "h_start", Type: NodeTypeStart},
			{
				ID:             "h_inspect",
				Type:           NodeTypeTask,
				TaskTemplateID: "run_health_inspection",
				OutputMapping:  map[string]string{"inspection_status": "health_status"},
			},
			{ID: "h_end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "he1", SourceID: "h_start", TargetID: "h_inspect"},
			{ID: "he2", SourceID: "h_inspect", TargetID: "h_end"},
		},
	}

	// 3. Define the Primary Master Consignment Workflow Definition
	masterDef := WorkflowDefinition{
		ID: "master_consignment_workflow",
		Nodes: []Node{
			{ID: "m_start", Type: NodeTypeStart},
			{
				ID:   "m_fanout_oga",
				Type: NodeTypeSplitTask,
				SplitTask: &SplitTaskConfig{
					Mode:            SplitModeDifferentTemplates,
					ItemsVariable:   "active_oga_requirements",
					ResultsVariable: "consolidated_oga_results",
					FailureMode:     FailureModeFailFast,
				},
			},
			{ID: "m_end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "me1", SourceID: "m_start", TargetID: "m_fanout_oga"},
			{ID: "me2", SourceID: "m_fanout_oga", TargetID: "m_end"},
		},
	}

	// 4. Mock Definition Hydration Activity Provider
	env.OnActivity("FetchWorkflowDefinitionActivity", mock.Anything, "oga_phyto_workflow").Return(phytoDef, nil)
	env.OnActivity("FetchWorkflowDefinitionActivity", mock.Anything, "oga_health_workflow").Return(healthDef, nil)

	// Mock Task Activity Processing Handlers
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "run_phyto_inspection", mock.Anything).Return(map[string]any{
		"inspection_status": "APPROVED_PHYTO",
	}, nil)

	env.OnActivity("ExecuteTaskActivity", mock.Anything, "run_health_inspection", mock.Anything).Return(map[string]any{
		"inspection_status": "APPROVED_HEALTH",
	}, nil)

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register nested sub-workflow runtime interpreter engine
	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})

	// 5. Establish initial runtime variables simulating dynamic lookup results
	initialVars := map[string]any{
		"active_oga_requirements": []map[string]any{
			{
				"template_id": "oga_phyto_workflow",
				"branch_id":   "oga-phyto",
				"payload": map[string]any{
					"container_id": "CONT-4412",
				},
			},
			{
				"template_id": "oga_health_workflow",
				"branch_id":   "oga-health",
				"payload": map[string]any{
					"container_id": "CONT-4412",
				},
			},
		},
	}

	// Execute Test Execution
	env.ExecuteWorkflow(GraphInterpreterWorkflow, masterDef, initialVars)

	// 6. Architectural Invariants Validation Checks
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var resultState WorkflowInstance
	err := env.GetWorkflowResult(&resultState)
	s.NoError(err)

	// Assert overall execution status
	s.Equal(StatusCompleted, resultState.Status)

	// Validate variable extraction aggregation results exist back on parent payload scope
	s.Contains(resultState.WorkflowVariables, "consolidated_oga_results")
	results, ok := resultState.WorkflowVariables["consolidated_oga_results"].([]any)
	s.True(ok)
	s.Len(results, 2)

	// Validate results content
	s.Equal("APPROVED_PHYTO", results[0].(map[string]any)["phyto_status"])
	s.Equal("APPROVED_HEALTH", results[1].(map[string]any)["health_status"])
}

func (s *NSWEngineTestSuite) TestDynamicFanOutWithSameTemplateMode() {
	env := s.NewTestWorkflowEnvironment()

	// Register activities
	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.FetchWorkflowDefinitionActivity, activity.RegisterOptions{Name: "FetchWorkflowDefinitionActivity"})

	// 1. Define a simple child workflow definition
	childDef := WorkflowDefinition{
		ID: "simple_child_workflow",
		Nodes: []Node{
			{ID: "c_start", Type: NodeTypeStart},
			{
				ID:             "c_task",
				Type:           NodeTypeTask,
				TaskTemplateID: "process_item",
				InputMapping:   map[string]string{"_iter.input.item_id": "item_id"},
				OutputMapping:  map[string]string{"processed_status": "status"},
			},
			{ID: "c_end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "ce1", SourceID: "c_start", TargetID: "c_task"},
			{ID: "ce2", SourceID: "c_task", TargetID: "c_end"},
		},
	}

	// 2. Define the Master Workflow Definition using SAME_TEMPLATE mode
	masterDef := WorkflowDefinition{
		ID: "master_same_template_workflow",
		Nodes: []Node{
			{ID: "m_start", Type: NodeTypeStart},
			{
				ID:             "m_fanout",
				Type:           NodeTypeSplitTask,
				TaskTemplateID: "simple_child_workflow", // Statically defined template ID for all branches
				SplitTask: &SplitTaskConfig{
					Mode:            SplitModeSameTemplate,
					ItemsVariable:   "items",
					ResultsVariable: "results",
					FailureMode:     FailureModeFailFast,
				},
			},
			{ID: "m_end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "me1", SourceID: "m_start", TargetID: "m_fanout"},
			{ID: "me2", SourceID: "m_fanout", TargetID: "m_end"},
		},
	}

	// 3. Mock Definition Hydration Activity Provider
	env.OnActivity("FetchWorkflowDefinitionActivity", mock.Anything, "simple_child_workflow").Return(childDef, nil)

	// Mock Task Activity Processing Handlers
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "process_item", mock.Anything).Return(map[string]any{
		"processed_status": "DONE_SUCCESS",
	}, nil)

	env.OnActivity("WorkflowCompletedActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register nested sub-workflow runtime interpreter engine
	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})

	// 4. Establish initial runtime variables (no template_id in the items!)
	initialVars := map[string]any{
		"items": []map[string]any{
			{
				"branch_id": "branch-1",
				"payload":   map[string]any{"item_id": "ITM-101"},
			},
			{
				"branch_id": "branch-2",
				"payload":   map[string]any{"item_id": "ITM-102"},
			},
		},
	}

	// Execute Test Execution
	env.ExecuteWorkflow(GraphInterpreterWorkflow, masterDef, initialVars)

	// 5. Architectural Invariants Validation Checks
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var resultState WorkflowInstance
	err := env.GetWorkflowResult(&resultState)
	s.NoError(err)

	// Assert overall execution status
	s.Equal(StatusCompleted, resultState.Status)

	// Validate variable extraction aggregation results exist back on parent payload scope
	s.Contains(resultState.WorkflowVariables, "results")
	results, ok := resultState.WorkflowVariables["results"].([]any)
	s.True(ok)
	s.Len(results, 2)
}

func (s *NSWEngineTestSuite) TestDynamicFanOutWithCollectAllFailures() {
	env := s.NewTestWorkflowEnvironment()

	// Register activities
	acts := &Activities{}
	env.RegisterActivityWithOptions(acts.ExecuteTaskActivity, activity.RegisterOptions{Name: "ExecuteTaskActivity"})
	env.RegisterActivityWithOptions(acts.WorkflowCompletedActivity, activity.RegisterOptions{Name: "WorkflowCompletedActivity"})
	env.RegisterActivityWithOptions(acts.FetchWorkflowDefinitionActivity, activity.RegisterOptions{Name: "FetchWorkflowDefinitionActivity"})

	// Define simple child workflow definition that will fail
	childDef := WorkflowDefinition{
		ID: "failing_child_workflow",
		Nodes: []Node{
			{ID: "c_start", Type: NodeTypeStart},
			{
				ID:             "c_task",
				Type:           NodeTypeTask,
				TaskTemplateID: "fail_task",
			},
			{ID: "c_end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "ce1", SourceID: "c_start", TargetID: "c_task"},
			{ID: "ce2", SourceID: "c_task", TargetID: "c_end"},
		},
	}

	// Define the Master Workflow Definition using COLLECT_ALL failure mode
	masterDef := WorkflowDefinition{
		ID: "master_collect_all_workflow",
		Nodes: []Node{
			{ID: "m_start", Type: NodeTypeStart},
			{
				ID:             "m_fanout",
				Type:           NodeTypeSplitTask,
				TaskTemplateID: "failing_child_workflow",
				SplitTask: &SplitTaskConfig{
					Mode:            SplitModeSameTemplate,
					ItemsVariable:   "items",
					ResultsVariable: "results",
					FailureMode:     FailureModeCollectAll,
				},
			},
			{ID: "m_end", Type: NodeTypeEnd},
		},
		Edges: []Edge{
			{ID: "me1", SourceID: "m_start", TargetID: "m_fanout"},
			{ID: "me2", SourceID: "m_fanout", TargetID: "m_end"},
		},
	}

	// Mock Definition Hydration Activity Provider
	env.OnActivity("FetchWorkflowDefinitionActivity", mock.Anything, "failing_child_workflow").Return(childDef, nil)

	// Mock Task Activity to fail
	env.OnActivity("ExecuteTaskActivity", mock.Anything, "fail_task", mock.Anything).Return(nil, errors.New("task failed intentionally"))

	// Register nested sub-workflow runtime interpreter engine
	env.RegisterWorkflowWithOptions(GraphInterpreterWorkflow, workflow.RegisterOptions{Name: "GraphInterpreterWorkflow"})

	initialVars := map[string]any{
		"items": []map[string]any{
			{
				"branch_id": "branch-1",
				"payload":   map[string]any{},
			},
			{
				"branch_id": "branch-2",
				"payload":   map[string]any{},
			},
		},
	}

	// Execute Test Execution
	env.ExecuteWorkflow(GraphInterpreterWorkflow, masterDef, initialVars)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "multiple branches failed")
	s.Contains(err.Error(), "branch-1-0 halted abnormally")
	s.Contains(err.Error(), "branch-2-1 halted abnormally")

	var resultState WorkflowInstance
	err = env.GetWorkflowResult(&resultState)
	s.Error(err) // Result retrieval fails because workflow failed

	// Retrieve status query directly from the workflow state mock to inspect variables
	val, queryErr := env.QueryWorkflow("GetStatus")
	s.NoError(queryErr)
	var instance WorkflowInstance
	s.NoError(val.Get(&instance))

	// Validate error state details are captured inside the results array on parent scope variables
	s.Contains(instance.WorkflowVariables, "results")
	results, ok := instance.WorkflowVariables["results"].([]any)
	s.True(ok)
	s.Len(results, 2)

	// Check that we captured the branch IDs and error messages correctly
	branch1Result, ok1 := results[0].(map[string]any)
	s.True(ok1)
	s.Equal("branch-1-0", branch1Result["branch_id"])
	s.Contains(branch1Result["error"], "task failed intentionally")

	branch2Result, ok2 := results[1].(map[string]any)
	s.True(ok2)
	s.Equal("branch-2-1", branch2Result["branch_id"])
	s.Contains(branch2Result["error"], "task failed intentionally")
}
