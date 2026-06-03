// Package engine implements a Temporal-based graph interpreter workflow engine.
package engine

import (
	"context"
	"fmt"
	"log/slog"

	"go.temporal.io/sdk/activity"
)

// Activities encapsulates the Temporal activity implementations utilized by the workflow engine.
// It maps the activity execution flow to custom callback handlers provided by the host application.
type Activities struct {
	// ExecuteTaskActivityHandler is invoked when the workflow engine encounters a task node.
	// - For synchronous execution, it should return a nil error with a map containing the results.
	// - For asynchronous execution, it should return a nil map and an ErrResultPending error,
	//   which pauses the workflow activity until an external handler triggers TaskDone.
	ExecuteTaskActivityHandler func(TaskPayload) (map[string]any, error)

	// WorkflowCompletedActivityHandler is invoked when the overall workflow execution succeeds and reaches
	// an End node. It receives the workflow ID and the final accumulated workflow variables, allowing the
	// host application to run any necessary completion triggers, notify listeners, or persist final state.
	WorkflowCompletedActivityHandler func(string, map[string]any) error

	// FetchWorkflowDefinitionHandler is invoked to dynamically retrieve the workflow definition structure
	// for a given template ID during SPLIT_TASK execution.
	FetchWorkflowDefinitionHandler func(templateID string) (WorkflowDefinition, error)
}

// FetchWorkflowDefinitionActivity is a Temporal activity that retrieves the workflow definition for a template ID.
func (a *Activities) FetchWorkflowDefinitionActivity(_ context.Context, templateID string) (WorkflowDefinition, error) {
	if a.FetchWorkflowDefinitionHandler == nil {
		return WorkflowDefinition{}, fmt.Errorf("FetchWorkflowDefinitionHandler is not initialized on Activities wrapper")
	}
	return a.FetchWorkflowDefinitionHandler(templateID)
}

// ExecuteTaskActivity pushes the task to your application and sleeps waiting for it or completes synchronously
func (a *Activities) ExecuteTaskActivity(ctx context.Context, taskTemplateID string, inputs map[string]any) (map[string]any, error) {
	info := activity.GetInfo(ctx)
	payload := TaskPayload{
		WorkflowID:     info.WorkflowExecution.ID,
		RunID:          info.WorkflowExecution.RunID,
		NodeID:         info.ActivityID, // this is Node.ID which was passed in workflow.WithActivityOptions(ctx, nodeActOpts)
		TaskTemplateID: taskTemplateID,
		Inputs:         inputs,
	}

	slog.Error("ExecuteTaskActivity", "payload", payload)

	// Trigger custom code block. ExecuteTaskActivityHandler can return error ErrResultPending to pause the workflow
	// or return a nil error with the outputs for the next step to consume (synchronous execution)
	res, err := a.ExecuteTaskActivityHandler(payload)
	if err != nil {
		return nil, err
	}

	// Return result immediately for synchronous steps
	return res, nil
}

// WorkflowCompletedActivity is a Temporal activity that executes when a workflow completes successfully.
func (a *Activities) WorkflowCompletedActivity(_ context.Context, workflowID string, finalContext map[string]any) error {
	return a.WorkflowCompletedActivityHandler(workflowID, finalContext)
}
