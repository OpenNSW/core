// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"fmt"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// completeActivationUpdate is the Workflow Update that delivers a suspended step's output to the
// node waiting on it. Manager.CompleteActivation sends it.
const completeActivationUpdate = "CompleteActivation"

// activationNotPendingErrorType is the type of the error the workflow rejects a completion with when
// the step it names isn't waiting. Manager.CompleteActivation turns it into ErrActivationNotPending.
const activationNotPendingErrorType = "ActivationNotPending"

// A step whose handler returns ErrResultPending ends its Activity at once, reporting Suspended, and
// its node waits here, in the workflow, for CompleteActivation. The wait is not an Activity, so it
// has no timeout: a step can wait for a person for as long as it takes, and the Activity's own
// timeout only has to cover running the plugin.
//
// pendingCompletions holds a Settable for each step waiting to be completed, keyed by its
// ActivationID. A node registers its step before it starts the Activity, so a completion that
// arrives while the Activity is still finishing is accepted, and waits for the node to read it.

// registerCompletionHandler installs the CompleteActivation Update handler. Its validator rejects a
// completion for a step that isn't waiting, and its handler takes the step out of pendingCompletions
// before anything else runs, so of several completions for one step exactly one is accepted and the
// rest are rejected: the same guarantee Temporal gave when completing an Activity.
func (g *graphInterpreter) registerCompletionHandler(ctx workflow.Context) error {
	return workflow.SetUpdateHandlerWithOptions(ctx, completeActivationUpdate,
		func(_ workflow.Context, activationID string, output map[string]any) error {
			settable, ok := g.pendingCompletions[activationID]
			if !ok {
				// The validator ran against the same state, with nothing in between.
				return activationNotPending(activationID)
			}
			delete(g.pendingCompletions, activationID)
			settable.Set(output, nil)
			return nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(_ workflow.Context, activationID string, _ map[string]any) error {
				if _, ok := g.pendingCompletions[activationID]; !ok {
					return activationNotPending(activationID)
				}
				return nil
			},
		},
	)
}

func activationNotPending(activationID string) error {
	return temporal.NewApplicationError(
		fmt.Sprintf("step %s is not waiting to be completed", activationID), activationNotPendingErrorType)
}

// expectCompletion registers activationID as a step that may be completed, and returns the Future its
// completion arrives on and a function that unregisters it. Call the function on every way out of the
// node: a step that completed synchronously, failed or was cancelled must stop accepting completions.
func (g *graphInterpreter) expectCompletion(ctx workflow.Context, activationID string) (workflow.Future, func()) {
	future, settable := workflow.NewFuture(ctx)
	g.pendingCompletions[activationID] = settable
	return future, func() { delete(g.pendingCompletions, activationID) }
}
