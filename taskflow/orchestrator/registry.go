package orchestrator

import (
	"encoding/json"

	engine "github.com/OpenNSW/go-temporal-workflow"
)

// TaskTemplateEntry defines the core common fields of any task configuration.
// All plugin-specific parameters are stored inside PluginProperties and decoded
// by each individual plugin.
type TaskTemplateEntry struct {
	ID               string          `json:"id"`
	TaskType         string          `json:"task_type"`         // e.g. "APPLICATION"
	PluginName       string          `json:"plugin_name"`       // e.g. "generic_user_input"
	PluginProperties json.RawMessage `json:"plugin_properties"` // plugin-specific config (like user_jsonforms_id, external_url)
}

// TaskTemplateRegistry is a simple in-process registry mapping template IDs to their config.
type TaskTemplateRegistry struct {
	entries          map[string]TaskTemplateEntry
	workflows        map[string]engine.WorkflowDefinition
	genericTemplates map[string]json.RawMessage
}

// NewTaskTemplateRegistry returns an empty registry.
// Call Register to add templates, or use NewTaskTemplateRegistryFromDir to load from JSON files.
func NewTaskTemplateRegistry() *TaskTemplateRegistry {
	return &TaskTemplateRegistry{
		entries:          make(map[string]TaskTemplateEntry),
		workflows:        make(map[string]engine.WorkflowDefinition),
		genericTemplates: make(map[string]json.RawMessage),
	}
}

func (r *TaskTemplateRegistry) Register(entry TaskTemplateEntry) {
	r.entries[entry.ID] = entry
}

func (r *TaskTemplateRegistry) Get(id string) (TaskTemplateEntry, bool) {
	entry, ok := r.entries[id]
	return entry, ok
}

func (r *TaskTemplateRegistry) RegisterWorkflow(def engine.WorkflowDefinition) {
	r.workflows[def.ID] = def
}

func (r *TaskTemplateRegistry) GetWorkflow(id string) (engine.WorkflowDefinition, bool) {
	def, ok := r.workflows[id]
	return def, ok
}

func (r *TaskTemplateRegistry) RegisterGenericTemplate(id string, raw json.RawMessage) {
	r.genericTemplates[id] = raw
}

func (r *TaskTemplateRegistry) GetGenericTemplate(id string) (json.RawMessage, bool) {
	raw, ok := r.genericTemplates[id]
	return raw, ok
}
