// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"encoding/json"
	"errors"
)

// TODO: github.com/OpenNSW/core is consolidating nsw-task-flow and
// go-temporal-workflow into core/taskflow (with its own TaskPlugin/
// PluginContext types), and this is generic engine-adapter code rather than
// anything Sri Lanka-specific. Once nsw-srilanka migrates its dependency from
// nsw-task-flow/go-temporal-workflow to core, move this file to
// core/taskflow/plugins (rebuilt against core's TaskPlugin/PluginContext) and
// delete this copy.

// ComputeFunc is synchronous computation logic that can be wrapped as a
// TaskPlugin via NewComputePlugin. It has the same shape
// as TaskPlugin.Execute, so any plain function can be registered without
// writing a dedicated plugin struct.
//
// It computes from the step's data and configuration alone, with no I/O, so
// an error it returns comes back on every retry: the plugin marks it
// Permanent and the step parks for an admin at once.
type ComputeFunc func(ctx PluginContext, config json.RawMessage) error

// computePlugin adapts an ComputeFunc to TaskPlugin —
// the function-to-interface adapter pattern (cf. http.HandlerFunc).
type computePlugin struct {
	fn ComputeFunc
}

// NewComputePlugin wraps fn as a TaskPlugin so it can be registered
// under any task type, e.g.:
//
//	pluginsRegistry.Register("HSCODE_SPLIT_BUILDER", trade.NewComputePlugin(trade.HscodeSplitBuilderFunc))
func NewComputePlugin(fn ComputeFunc) TaskPlugin {
	return &computePlugin{fn: fn}
}

func (p *computePlugin) Execute(ctx PluginContext, config json.RawMessage) error {
	err := p.fn(ctx, config)
	if err == nil || errors.Is(err, ErrSuspended) || IsPermanent(err) {
		return err
	}
	return Permanent(ErrorTypeInput, err)
}
