// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/OpenNSW/core/shared/deepcopy"
)

// TaskRecord is the single DB entry per task instance, as described in the architecture doc.
// It stores both Parent (macro journey) and Task (active sub-process) coordinates separately,
// and holds dynamic task execution data as a generic key-value map.
type TaskRecord struct {
	TaskID       string          `json:"task_id"`
	TaskType     string          `json:"task_type"`
	State        string          `json:"status"` // State drives UI rendering ("PENDING_USER", "QUEUED_EXTERNALLY", "COMPLETED")
	RenderConfig json.RawMessage `json:"render_config"`

	// Parent coordinates — used to wake the parent workflow when this task finishes.
	// There is no ParentRunID: the parent workflow never has more than one run (this engine never
	// uses Continue-As-New, workflow-level retries, or ID reuse), so "" always correctly addresses
	// whichever run is current — see Manager.CompleteActivation.
	ParentWorkflowID string `json:"parent_workflow_id"`
	ParentStepID     string `json:"parent_step_id"` // the parent's step ID: the Activity to complete

	RootWorkflowID string `json:"root_workflow_id"`

	// Active step execution coordinates — used to resume/wake the currently active step via the API.
	// WARNING: Since the store only holds a single set of coordinates, only one step can be active at any given time
	// (strictly sequential execution). Parallel/concurrent steps inside a single Task Workflow are not supported.
	TaskWorkflowID       string `json:"task_workflow_id"`
	ActiveTaskTemplateID string `json:"active_task_template_id,omitempty"`

	// ActiveStepID identifies the run of the step node that is currently active. Empty until the
	// first step is claimed. It, with Seq, is written only by the guarded TaskStore methods below,
	// never by InitTask, so a stale full-record save cannot move it backwards.
	ActiveStepID string `json:"active_step_id,omitempty"`
	// Seq is the version of the task row: the workflow-wide step counter, advanced by the guarded
	// writes. Guards compare against it so a write from an earlier step is dropped. Exposed
	// read-only to clients as a version; it is never accepted as input.
	Seq int64 `json:"seq"`

	// Data holds generic, dynamic task execution state variables.
	Data map[string]any `json:"data"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DeepCopy returns a copy of the record that shares no mutable reference state
// (maps, slices, raw-JSON byte buffers) with the original. Scalar fields are
// duplicated by the value copy; the reference-typed fields are cloned
// explicitly so a caller handed the copy cannot mutate the source.
func (r TaskRecord) DeepCopy() TaskRecord {
	cp := r // value copy duplicates all scalar fields
	cp.RenderConfig = copyBytes(r.RenderConfig)
	cp.Data = deepcopy.Map(r.Data)
	return cp
}

// copyBytes returns a copy of b, or nil if b is nil.
func copyBytes(b json.RawMessage) json.RawMessage {
	if b == nil {
		return nil
	}
	return append(json.RawMessage(nil), b...)
}

// Task states written by the guarded step statements.
const (
	// StateStartingStep means a step has been claimed and its plugin has not yet reported the state
	// to render.
	StateStartingStep = "STARTING_STEP"
	// StateCompleted means the task workflow has ended.
	StateCompleted = "COMPLETED"
	// StateAdvancing means the active step's submission was accepted by the workflow, which is now
	// moving to the next node. Clients keep the view until the state moves on.
	StateAdvancing = "ADVANCING"
)

// StepClaim is the row change that makes a step the task's active step.
type StepClaim struct {
	// StepID is the ID of this run of the node, unique per run.
	StepID string
	// Seq is the workflow-wide step counter value for this run.
	Seq int64
	// ActiveTaskTemplateID is the step template the step runs.
	ActiveTaskTemplateID string
	// State is the render state to show while the step starts.
	State string
	// Data replaces the task data. It is set from the step's inputs, not merged.
	Data map[string]any
}

// TaskStore is an interface that any persistent or in-memory database used by the TaskManager should implement.
//
// The four step methods below are conditional writes. Each is one atomic statement whose guard is
// part of the write, so a write from a step that is no longer current changes nothing. Each returns
// the number of rows changed: 0 means the write was stale and was dropped, which is not an error.
// InitTask must not write ActiveStepID or Seq.
type TaskStore interface {
	// InitTask creates the row for a new task (state, parent coordinates, render config snapshot).
	// It is called once, by StartTask, but must be idempotent: StartTask is a Temporal Activity and
	// can be retried, so a second call for the same TaskID re-applies the same coarse fields rather
	// than failing on a conflict.
	InitTask(context context.Context, record TaskRecord)
	GetTask(context context.Context, taskID string) (TaskRecord, bool)
	GetTaskByWorkflowID(context context.Context, workflowID string) (TaskRecord, bool)
	GetAllTasks(context context.Context, parentWorkflowID string) []TaskRecord

	// ClaimStep makes claim.StepID the active step. Guard: the stored seq is <= claim.Seq, so a
	// retry of the same step matches again and an older step's late attempt does not.
	ClaimStep(context context.Context, taskID string, claim StepClaim) (int64, error)
	// WriteRenderState sets the state and data the step shows once its plugin has run. Guard: the
	// active step is stepID and the stored seq equals seq.
	WriteRenderState(context context.Context, taskID, stepID string, seq int64, state string, data map[string]any) (int64, error)
	// PersistSubmission stores the data submitted for the active step, sets StateAdvancing and
	// advances seq to seq+1. Guard: the active step is stepID and the stored seq equals seq.
	PersistSubmission(context context.Context, taskID, stepID string, seq int64, data map[string]any) (int64, error)
	// CompleteTask sets StateCompleted and seq. Guard: the stored seq is <= seq.
	CompleteTask(context context.Context, taskID string, seq int64) (int64, error)
}
