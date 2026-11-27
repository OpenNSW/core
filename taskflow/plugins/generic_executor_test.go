// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// An executor computes from the step's data alone, so its error comes back on
// every retry and the step parks at once.
func TestGenericExecutorPlugin_ErrorsArePermanent(t *testing.T) {
	failing := NewGenericExecutorPlugin(func(PluginContext, json.RawMessage) error {
		return errors.New("hs_codes cannot be empty")
	})
	err := failing.Execute(PluginContext{}, nil)
	errType, permanent := PermanentType(err)
	assert.True(t, permanent, "err = %v", err)
	assert.Equal(t, ErrorTypeInput, errType)
	assert.EqualError(t, err, "hs_codes cannot be empty")
}

func TestGenericExecutorPlugin_PassesThroughSuccessSuspensionAndItsOwnVerdict(t *testing.T) {
	run := func(err error) error {
		return NewGenericExecutorPlugin(func(PluginContext, json.RawMessage) error { return err }).
			Execute(PluginContext{}, nil)
	}
	assert.NoError(t, run(nil))
	assert.Equal(t, ErrSuspended, run(ErrSuspended))

	own := Permanent(ErrorTypeConfig, errors.New("no mappings configured"))
	assert.Equal(t, own, run(own), "an error the function already marked keeps its type")
}
