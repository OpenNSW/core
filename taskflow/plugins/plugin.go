package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/OpenNSW/nsw-task-flow/store"
)

// PluginContext provides the database record, input arguments, and context
// to a plugin during execution.
type PluginContext struct {
	Context context.Context
	Record  *store.TaskRecord
	Inputs  map[string]any
}

// TaskPlugin is the interface that all interaction and system action handlers must implement.
type TaskPlugin interface {
	// Name returns the unique identifier for the plugin (e.g. "generic_user_input").
	Name() string

	// Execute runs the custom logic of the plugin, updating the task record status and metadata.
	// The config argument contains the custom plugin configuration parameters unmarshaled from JSON.
	Execute(ctx PluginContext, config json.RawMessage) error
}

// Registry is a thread-safe registry of task plugins keyed by taskType and pluginName.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]TaskPlugin
}

// NewRegistry creates a new, empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]TaskPlugin),
	}
}

// Register adds a new plugin for a specific taskType. It returns an error if a plugin with the same name already exists for that taskType.
func (r *Registry) Register(taskType string, p TaskPlugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := fmt.Sprintf("%s/%s", taskType, p.Name())
	if _, exists := r.plugins[key]; exists {
		return fmt.Errorf("plugin with name %q is already registered for task type %q", p.Name(), taskType)
	}

	r.plugins[key] = p
	return nil
}

// Get retrieves a registered plugin by taskType and pluginName.
func (r *Registry) Get(taskType string, name string) (TaskPlugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := fmt.Sprintf("%s/%s", taskType, name)
	p, exists := r.plugins[key]
	return p, exists
}
