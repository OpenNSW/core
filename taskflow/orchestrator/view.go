// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"encoding/json"
	"time"
)

// TaskView represents a purely presentational view of a task for the frontend.
// It removes all internal Temporal coordinates and exposes only what the UI needs.
type TaskView struct {
	TaskID   string `json:"task_id"`
	TaskType string `json:"task_type"`
	State    string `json:"state"`
	// StepID is the step the task is currently on. A caller acting on the task (CompleteTaskStep)
	// echoes it back, so an action taken on a view the task has since moved past is rejected as
	// stale instead of being applied to the step that is active now. Empty until the first step
	// starts.
	StepID string `json:"step_id,omitempty"`
	// Version increases every time the task moves on. It is read-only: clients use it to keep the
	// newest of several views they fetched, and it is never accepted as input.
	Version   int64           `json:"version"`
	View      json.RawMessage `json:"view,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}
