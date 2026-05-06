package store

import "time"

// TaskRecord is the single DB entry per task instance, as described in the architecture doc.
// It stores both Parent (macro journey) and Task (active sub-process) coordinates separately,
// and holds dynamic task execution data as a generic key-value map.
type TaskRecord struct {
	TaskID         string `json:"task_id"`
	TaskType       string `json:"task_type"`
	UserFormID     string `json:"user_form_id"`
	ReviewerFormID string `json:"reviewer_form_id"`
	// Status drives UI rendering ("PENDING_USER", "QUEUED_EXTERNALLY", "COMPLETED")
	Status string `json:"status"`

	// Parent coordinates — used to wake the parent workflow when this task finishes
	ParentWorkflowID string `json:"parent_workflow_id"`
	ParentRunID      string `json:"parent_run_id"`
	ParentNodeID     string `json:"parent_node_id"`

	// Active subtask execution coordinates — used to resume/wake the currently active subtask step via the API.
	// WARNING: Since the store only holds a single set of coordinates, only one subtask can be active at any given time
	// (strictly sequential execution). Parallel/concurrent subtasks inside a single Task Workflow are not supported.
	TaskWorkflowID   string `json:"task_workflow_id"`
	TaskRunID        string `json:"task_run_id"`
	ActiveActivityID string `json:"active_activity_id"`

	// Data holds generic, dynamic task execution state variables.
	Data map[string]any `json:"data"`

	CreatedAt time.Time `json:"created_at"`
}

// TaskStore is an interface that any persistent or in-memory database used by the TaskManager should implement.
type TaskStore interface {
	SaveTask(record TaskRecord)
	GetTask(taskID string) (TaskRecord, bool)
	GetTaskByWorkflowID(workflowID string) (TaskRecord, bool)
	GetAllTasks() []TaskRecord
}
