package trade

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/stretchr/testify/assert"
)

// An executor computes from the step's data alone, so its error comes back on
// every retry and the step parks at once.
func TestGenericExecutorPlugin_ErrorsArePermanent(t *testing.T) {
	failing := NewGenericExecutorPlugin(func(plugins.PluginContext, json.RawMessage) error {
		return errors.New("hs_codes cannot be empty")
	})
	err := failing.Execute(plugins.PluginContext{}, nil)
	errType, permanent := plugins.PermanentType(err)
	assert.True(t, permanent, "err = %v", err)
	assert.Equal(t, plugins.ErrorTypeInput, errType)
	assert.EqualError(t, err, "hs_codes cannot be empty")
}

func TestGenericExecutorPlugin_PassesThroughSuccessSuspensionAndItsOwnVerdict(t *testing.T) {
	run := func(err error) error {
		return NewGenericExecutorPlugin(func(plugins.PluginContext, json.RawMessage) error { return err }).
			Execute(plugins.PluginContext{}, nil)
	}
	assert.NoError(t, run(nil))
	assert.Equal(t, plugins.ErrSuspended, run(plugins.ErrSuspended))

	own := plugins.Permanent(plugins.ErrorTypeConfig, errors.New("no mappings configured"))
	assert.Equal(t, own, run(own), "an error the function already marked keeps its type")
}
