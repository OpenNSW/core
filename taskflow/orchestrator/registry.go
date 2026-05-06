package orchestrator

import "encoding/json"

// TaskTemplateEntry defines the core common fields of any task configuration.
// All plugin-specific parameters are stored inside PluginProperties and decoded
// by each individual plugin.
type TaskTemplateEntry struct {
	TemplateID       string          `json:"template_id"`
	TaskType         string          `json:"task_type"` // e.g. "APPLICATION"
	WorkflowID       string          `json:"workflow_id"`
	PluginName       string          `json:"plugin_name"`       // e.g. "generic_user_input"
	PluginProperties json.RawMessage `json:"plugin_properties"` // plugin-specific config (like user_jsonforms_id, external_url)
}

// TaskTemplateRegistry is a simple in-process registry mapping template IDs to their config.
type TaskTemplateRegistry struct {
	entries map[string]TaskTemplateEntry
}

// NewTaskTemplateRegistry returns an empty registry.
// Call Register to add templates, or use NewTaskTemplateRegistryFromDir to load from JSON files.
func NewTaskTemplateRegistry() *TaskTemplateRegistry {
	return &TaskTemplateRegistry{entries: make(map[string]TaskTemplateEntry)}
}

func (r *TaskTemplateRegistry) Register(entry TaskTemplateEntry) {
	r.entries[entry.TemplateID] = entry
}

func (r *TaskTemplateRegistry) Get(templateID string) (TaskTemplateEntry, bool) {
	entry, ok := r.entries[templateID]
	return entry, ok
}
