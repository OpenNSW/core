// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A compute step works from the step's data alone, so its error comes back on
// every retry and the step parks at once.
func TestComputePlugin_ErrorsArePermanent(t *testing.T) {
	failing := NewComputePlugin(func(PluginContext, json.RawMessage) error {
		return errors.New("line_items cannot be empty")
	})
	err := failing.Execute(PluginContext{}, nil)
	errType, permanent := PermanentType(err)
	assert.True(t, permanent, "err = %v", err)
	assert.Equal(t, ErrorTypeInput, errType)
	assert.EqualError(t, err, "line_items cannot be empty")
}

func TestComputePlugin_PassesThroughSuccessSuspensionAndItsOwnVerdict(t *testing.T) {
	run := func(err error) error {
		return NewComputePlugin(func(PluginContext, json.RawMessage) error { return err }).
			Execute(PluginContext{}, nil)
	}
	assert.NoError(t, run(nil))
	assert.Equal(t, ErrSuspended, run(ErrSuspended))

	own := Permanent(ErrorTypeConfig, errors.New("no mappings configured"))
	assert.Equal(t, own, run(own), "an error the function already marked keeps its type")
}

func TestComputePlugin_PassesConfigAndContext(t *testing.T) {
	var gotConfig json.RawMessage
	var gotInputs map[string]any
	p := NewComputePlugin(func(ctx PluginContext, config json.RawMessage) error {
		gotConfig, gotInputs = config, ctx.Inputs
		return nil
	})
	cfg := json.RawMessage(`{"mappings":{"a":["b"]}}`)
	inputs := map[string]any{"line_items": []any{"a"}}
	assert.NoError(t, p.Execute(PluginContext{Inputs: inputs}, cfg))
	assert.JSONEq(t, string(cfg), string(gotConfig))
	assert.Equal(t, inputs, gotInputs)
}
