// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package extensions

import (
	"context"

	"github.com/OpenNSW/core/artifact/adapter/types"
)

// Invocation describes the lifecycle event an extension is running for. The orchestrator puts it
// on the context passed to TaskExtension.Execute; read it with InvocationFrom.
//
// Most extensions do not need it: an extension does one job whatever phase it is declared in. It
// is there for the ones that must tell repeats apart, such as a notification that must not be sent
// twice.
type Invocation struct {
	// Phase is the lifecycle phase the extension was declared in and is running for.
	Phase types.ExecutionPhase
	// IdempotencyKey names one occurrence of Phase: the same task, step and seq give the same
	// key, so an extension that runs again for an event it already handled can recognise it. It
	// has the form "<task id>/<step id>/<seq>/<phase>".
	//
	// A guard phase (PRE_RESUME) may run several times under one key, once for each call it
	// checks (every draft save and every refused submission of the step, for example), so a guard
	// must not dedupe on it. A notify phase (POST_RESUME) runs after the change it reports was
	// committed and should be treated as at-least-once: dedupe on this key.
	IdempotencyKey string
}

type invocationKey struct{}

// WithInvocation returns a copy of ctx carrying inv. The orchestrator calls it before running an
// extension; tests of an extension can use it to supply one.
func WithInvocation(ctx context.Context, inv Invocation) context.Context {
	return context.WithValue(ctx, invocationKey{}, inv)
}

// InvocationFrom returns the Invocation ctx carries, and false if it carries none (an extension
// called outside the orchestrator).
func InvocationFrom(ctx context.Context) (Invocation, bool) {
	inv, ok := ctx.Value(invocationKey{}).(Invocation)
	return inv, ok
}
