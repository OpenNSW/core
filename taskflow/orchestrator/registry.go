package orchestrator

import (
	"encoding/json"

	engine "github.com/OpenNSW/core/workflow"
)

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
	OutputNamespace  string          `json:"output_namespace,omitempty"` // top-level slot in TaskRecord.Data where CompleteTaskStep payloads are written. Empty falls back to a flat top-level merge.
}

// TaskTemplateRegistry is the read-only contract the orchestrator depends on
// for resolving task definitions, subtask definitions, child workflows, and
// generic JSON templates. Library consumers supply their own implementation
// (in-memory, DB-backed, remote, etc.).
type TaskTemplateRegistry interface {
	GetTaskTemplate(id string) (TaskTemplate, bool)
	GetSubTaskTemplate(id string) (SubTaskTemplate, bool)
	GetWorkflow(id string) (engine.WorkflowDefinition, bool)
	GetGenericTemplate(id string) (json.RawMessage, bool)
}
