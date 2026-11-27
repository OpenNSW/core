// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"encoding/json"
	"errors"
)

// ComputeFunc is a step that computes from its data and configuration alone,
// with no I/O. It has the same shape as TaskPlugin.Execute, so a plain function
// can be registered without writing a plugin struct for it.
//
// An error it returns comes back on every retry, so NewComputePlugin marks it
// Permanent and the step parks for an admin at once. A step that calls another
// service needs its own plugin, which classifies that service's errors so a
// failure that may pass is retried.
type ComputeFunc func(ctx PluginContext, config json.RawMessage) error

// computePlugin adapts a ComputeFunc to TaskPlugin, as http.HandlerFunc adapts
// a function to http.Handler.
type computePlugin struct {
	fn ComputeFunc
}

// NewComputePlugin wraps fn as a TaskPlugin so it can be registered under any
// task type, e.g.:
//
//	registry.Register("ORDER_SPLIT", plugins.NewComputePlugin(splitOrder))
//
// An error fn returns is marked Permanent with ErrorTypeInput, unless it is
// ErrSuspended or fn already marked it Permanent itself.
// A nil fn fails every run with ErrorTypeConfig.
func NewComputePlugin(fn ComputeFunc) TaskPlugin {
	return &computePlugin{fn: fn}
}

func (p *computePlugin) Execute(ctx PluginContext, config json.RawMessage) error {
	if p.fn == nil {
		return Permanent(ErrorTypeConfig, errors.New("compute plugin: no function to run"))
	}
	err := p.fn(ctx, config)
	if err == nil || errors.Is(err, ErrSuspended) || IsPermanent(err) {
		return err
	}
	return Permanent(ErrorTypeInput, err)
}
