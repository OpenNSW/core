package tasktypes

import (
	"context"

	"github.com/OpenNSW/core/remote"
	flowplugins "github.com/OpenNSW/core/taskflow/plugins"
)

// dispatchHelper bundles outbound HTTP behaviour shared by plugins in this
// package.
type dispatchHelper struct {
	manager *remote.Manager
}

func newDispatchHelper(manager *remote.Manager) *dispatchHelper {
	return &dispatchHelper{manager: manager}
}

// callbacksPath is where a service accepts a callback on one of its parked steps:
// POST {callbacksPath}/{token}.
const callbacksPath = "/api/v1/callbacks"

// post sends body as JSON to the resolved service+path. Errors always
// propagate: this dispatch is a precondition for ErrSuspended (the caller is
// about to park the subtask awaiting a callback that only the receiving
// service can send), so a failed dispatch must fail the step rather than
// suspend it — the task engine's own retry is what lets the workflow recover
// once the receiving service comes up.
func (h *dispatchHelper) post(ctx context.Context, serviceID, path string, body any) error {
	req := remote.Request{
		Method: "POST",
		Path:   path,
		Body:   remote.JSONBody{V: body},
	}
	return h.manager.Call(ctx, serviceID, req, nil)
}

type pluginContext = flowplugins.PluginContext

// configError marks err as a configuration error retrying can't fix, so the step
// parks for an admin at once (see flowplugins.Permanent).
func configError(err error) error {
	return flowplugins.Permanent(flowplugins.ErrorTypeConfig, err)
}

// inputError marks err as an error in the step's data that retrying can't fix:
// the inputs are the same on every attempt (see flowplugins.Permanent).
func inputError(err error) error {
	return flowplugins.Permanent(flowplugins.ErrorTypeInput, err)
}

// classifyRemote marks a remote service's refusal (a 4xx other than 408 or 429)
// as an error retrying can't fix, and leaves every other error retryable (see
// flowplugins.ClassifyRemote).
func classifyRemote(err error) error {
	return flowplugins.ClassifyRemote(err)
}

// ErrSuspended signals to the orchestrator that this plugin step is parked and
// waiting for an external callback before the sub-workflow can advance.
var ErrSuspended = flowplugins.ErrSuspended
