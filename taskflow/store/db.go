// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/OpenNSW/core/internal/deepcopy"
)

// TaskRecord is the single DB entry per task instance, as described in the architecture doc.
// It stores both Parent (macro journey) and Task (active sub-process) coordinates separately,
// and holds dynamic task execution data as a generic key-value map.
type TaskRecord struct {
	TaskID       string          `json:"task_id"`
	TaskType     string          `json:"task_type"`
	State        string          `json:"status"` // State drives UI rendering ("PENDING_USER", "QUEUED_EXTERNALLY", "COMPLETED")
	RenderConfig json.RawMessage `json:"render_config"`

	// Parent coordinates — used to wake the parent workflow when this task finishes
	ParentWorkflowID string `json:"parent_workflow_id"`
	ParentRunID      string `json:"parent_run_id"`
	ParentNodeID     string `json:"parent_node_id"`

	RootWorkflowID string `json:"root_workflow_id"` // top-level consignment ID

	// Active subtask execution coordinates — used to resume/wake the currently active subtask step via the API.
	// WARNING: Since the store only holds a single set of coordinates, only one subtask can be active at any given time
	// (strictly sequential execution). Parallel/concurrent subtasks inside a single Task Workflow are not supported.
	TaskWorkflowID       string `json:"task_workflow_id"`
	TaskRunID            string `json:"task_run_id"`
	SubTaskNodeID        string `json:"subtask_node_id"`
	ActiveTaskTemplateID string `json:"active_task_template_id,omitempty"`

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

// TaskStore is an interface that any persistent or in-memory database used by the TaskManager should implement.
type TaskStore interface {
	SaveTask(context context.Context, record TaskRecord)
	GetTask(context context.Context, taskID string) (TaskRecord, bool)
	GetTaskByWorkflowID(context context.Context, workflowID string) (TaskRecord, bool)
	GetAllTasks(context context.Context, parentWorkflowID string) []TaskRecord
}
