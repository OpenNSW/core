package types

import "encoding/json"

// TaskTemplate describes a Task — the macro unit of work activated by a parent
// workflow. A Task runs a child workflow whose nodes invoke SubTaskTemplates.
type TaskTemplate struct {
	ID             string `json:"id"`
	Type           string `json:"type"`             // user-facing category (e.g. "APPLICATION")
	WorkflowID     string `json:"workflow_id"`      // points at a registered engine.WorkflowDefinition
	RenderConfigID string `json:"render_config_id"` // task-level render config
}

// SubTaskTemplate describes a SubTask — an individual execution step inside a
// Task's workflow that delegates to a plugin.
type SubTaskTemplate struct {
	ID               string          `json:"id"`
	TaskType         string          `json:"task_type"`                  // plugin routing key (e.g. "USER_INPUT")
	PluginProperties json.RawMessage `json:"plugin_properties"`          // plugin-specific config
	OutputNamespace  string          `json:"output_namespace,omitempty"` // top-level slot in TaskRecord.Data where CompleteTaskStep payloads are written
}
