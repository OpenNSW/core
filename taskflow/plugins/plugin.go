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

// Registry is a thread-safe registry of task plugins.
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

// Register adds a new plugin to the registry. It returns an error if a plugin with the same name already exists.
func (r *Registry) Register(p TaskPlugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := p.Name()
	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("plugin with name %q is already registered", name)
	}

	r.plugins[name] = p
	return nil
}

// Get retrieves a registered plugin by name.
func (r *Registry) Get(name string) (TaskPlugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, exists := r.plugins[name]
	return p, exists
}
