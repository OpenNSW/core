// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package engine implements a Temporal-based graph interpreter workflow engine.
package engine

import (
	"context"
	"errors"
	"fmt"

	"go.temporal.io/sdk/activity"
)

// Activities encapsulates the Temporal activity implementations utilized by the workflow engine.
// It maps the activity execution flow to custom callback handlers provided by the host application.
type Activities struct {
	// ExecuteTaskActivityHandler is invoked when the workflow engine encounters a task node. See
	// TaskActivationHandler.
	ExecuteTaskActivityHandler TaskActivationHandler

	// WorkflowCompletedActivityHandler is invoked when the overall workflow execution succeeds and reaches
	// an End node. It receives the workflow ID and the final accumulated workflow variables, allowing the
	// host application to run any necessary completion triggers, notify listeners, or persist final state.
	WorkflowCompletedActivityHandler WorkflowCompletionHandler

	// FetchWorkflowDefinitionHandler is invoked to dynamically retrieve the workflow definition structure
	// for a given template ID during SPLIT_TASK execution.
	FetchWorkflowDefinitionHandler DefinitionHandler

	// AdminParkHandler is invoked whenever a node parks for admin intervention. See the
	// AdminParkHandler type doc for what a nil handler means.
	AdminParkHandler AdminParkHandler
}

// FetchWorkflowDefinitionActivity is a Temporal activity that retrieves the workflow definition for
// a template ID, from the artifact manifest version the calling workflow resolves through (see
// VarManifestVersion).
func (a *Activities) FetchWorkflowDefinitionActivity(_ context.Context, templateID string, manifestVersion int) (WorkflowDefinition, error) {
	if a.FetchWorkflowDefinitionHandler == nil {
		return WorkflowDefinition{}, fmt.Errorf("FetchWorkflowDefinitionHandler is not initialized on Activities wrapper")
	}
	return a.FetchWorkflowDefinitionHandler(manifestVersion, templateID)
}

// ActivationRef is what the workflow tells ExecuteTaskActivity about the run it is starting, beyond what
// the Activity's own info carries.
type ActivationRef struct {
	// NodeID is the node's ID in the workflow definition (Node.ID).
	NodeID string `json:"node_id"`
	// Seq is the workflow-wide step counter value for this run.
	Seq int64 `json:"seq"`
	// ManifestVersion is the workflow's artifact manifest version (see VarManifestVersion).
	ManifestVersion int `json:"manifest_version,omitempty"`
}

// TaskResult is what ExecuteTaskActivity reports to the workflow.
type TaskResult struct {
	// Suspended is true when the step is waiting for a completion (the handler returned
	// ErrResultPending). The workflow then waits for CompleteActivation, outside the Activity.
	Suspended bool `json:"suspended,omitempty"`
	// Output is the step's result when it completed synchronously.
	Output map[string]any `json:"output,omitempty"`
}

// ExecuteTaskActivity hands the task to the host application. It returns the step's output when the
// handler completes it synchronously, and Suspended when the handler returns ErrResultPending: the
// Activity ends either way, and a suspended step's completion reaches the workflow through
// CompleteActivation, not through this Activity.
func (a *Activities) ExecuteTaskActivity(ctx context.Context, taskTemplateID string, inputs map[string]any, caseID string, step ActivationRef) (TaskResult, error) {
	info := activity.GetInfo(ctx)
	payload := TaskPayload{
		WorkflowID:      info.WorkflowExecution.ID,
		RunID:           info.WorkflowExecution.RunID,
		NodeID:          step.NodeID,
		ActivationID:    info.ActivityID, // the step ID the workflow passed as the ActivityID
		Seq:             step.Seq,
		TaskTemplateID:  taskTemplateID,
		ManifestVersion: step.ManifestVersion,
		Inputs:          inputs,
		CaseID:          caseID,
	}

	res, err := a.ExecuteTaskActivityHandler(ctx, payload)
	if errors.Is(err, activity.ErrResultPending) {
		return TaskResult{Suspended: true}, nil
	}
	if err != nil {
		return TaskResult{}, err
	}
	return TaskResult{Output: res}, nil
}

// WorkflowCompletedActivity is a Temporal activity that executes when a workflow completes successfully.
func (a *Activities) WorkflowCompletedActivity(ctx context.Context, workflowID string, finalContext map[string]any, seq int64) error {
	return a.WorkflowCompletedActivityHandler(ctx, WorkflowCompletion{WorkflowID: workflowID, Seq: seq, FinalVariables: finalContext})
}

// AdminParkActivity is a Temporal activity that notifies the host application whenever a node
// parks for admin intervention. It is a no-op if no AdminParkHandler is registered — unlike the
// other handlers here, notification is optional, so an unset handler is not an error.
func (a *Activities) AdminParkActivity(_ context.Context, payload AdminParkPayload) error {
	if a.AdminParkHandler == nil {
		return nil
	}
	return a.AdminParkHandler(payload)
}
