package trade

import (
	"encoding/json"
	"errors"

	"github.com/OpenNSW/core/taskflow/plugins"
)

// TODO: github.com/OpenNSW/core is consolidating nsw-task-flow and
// go-temporal-workflow into core/taskflow (with its own TaskPlugin/
// PluginContext types), and this is generic engine-adapter code rather than
// anything Sri Lanka-specific. Once nsw-srilanka migrates its dependency from
// nsw-task-flow/go-temporal-workflow to core, move this file to
// core/taskflow/plugins (rebuilt against core's TaskPlugin/PluginContext) and
// delete this copy.

// ExecutorFunc is synchronous computation logic that can be wrapped as a
// plugins.TaskPlugin via NewGenericExecutorPlugin. It has the same shape
// as TaskPlugin.Execute, so any plain function can be registered without
// writing a dedicated plugin struct.
//
// It computes from the step's data and configuration alone, with no I/O, so
// an error it returns comes back on every retry: the plugin marks it
// plugins.Permanent and the step parks for an admin at once.
type ExecutorFunc func(ctx plugins.PluginContext, config json.RawMessage) error

// genericExecutorPlugin adapts an ExecutorFunc to plugins.TaskPlugin —
// the function-to-interface adapter pattern (cf. http.HandlerFunc).
type genericExecutorPlugin struct {
	fn ExecutorFunc
}

// NewGenericExecutorPlugin wraps fn as a TaskPlugin so it can be registered
// under any task type, e.g.:
//
//	pluginsRegistry.Register("HSCODE_SPLIT_BUILDER", trade.NewGenericExecutorPlugin(trade.HscodeSplitBuilderFunc))
func NewGenericExecutorPlugin(fn ExecutorFunc) plugins.TaskPlugin {
	return &genericExecutorPlugin{fn: fn}
}

func (p *genericExecutorPlugin) Execute(ctx plugins.PluginContext, config json.RawMessage) error {
	err := p.fn(ctx, config)
	if err == nil || errors.Is(err, plugins.ErrSuspended) || plugins.IsPermanent(err) {
		return err
	}
	return plugins.Permanent(plugins.ErrorTypeInput, err)
}
