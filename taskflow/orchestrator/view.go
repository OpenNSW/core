package orchestrator

import (
	"encoding/json"
	"time"
)

// TaskView represents a purely presentational view of a task for the frontend.
// It removes all internal Temporal coordinates and exposes only what the UI needs.
type TaskView struct {
	TaskID    string          `json:"task_id"`
	TaskType  string          `json:"task_type"`
	State     string          `json:"state"`
	View      json.RawMessage `json:"view,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}
