package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/OpenNSW/core/taskflow/store"
)

// ErrSuspended is returned by a plugin when it wants to pause task execution and wait for an asynchronous action.
var ErrSuspended = errors.New("activity result pending")

// PluginContext provides the database record, input arguments, and context
// to a plugin during execution.
type PluginContext struct {
	Context context.Context
	Record  *store.TaskRecord
	Inputs  map[string]any
}

// TaskPlugin is the interface that all interaction and system action handlers must implement.
type TaskPlugin interface {
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

	if _, exists := r.plugins[taskType]; exists {
		return fmt.Errorf("plugin with name %q is already registered for task type", taskType)
	}

	r.plugins[taskType] = p
	return nil
}

// Get retrieves a registered plugin by taskType and pluginName.
func (r *Registry) Get(taskType string) (TaskPlugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, exists := r.plugins[taskType]
	return p, exists
}
