// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"fmt"
	"strconv"

	"github.com/OpenNSW/core/artifact/adapter/types"
	"github.com/OpenNSW/core/shared/deepcopy"
	"github.com/OpenNSW/core/taskflow/extensions"
	"github.com/OpenNSW/core/taskflow/store"
)

// Dispatcher runs the extensions of a notify phase (POST_RESUME) after the orchestrator has
// committed the change they report. Dispatch must not block the caller on run: the operation that
// fired the phase has already succeeded and returns as soon as Dispatch does.
//
// run executes every extension the phase declares, in order, and logs their errors; it never
// fails. The context passed to Dispatch carries the caller's values but is never cancelled.
//
// The default runs each dispatch in its own goroutine, best effort: a process that stops before
// run finishes loses it. Supply your own with WithDispatcher to bound concurrency, recover panics,
// or run notify phases synchronously in tests.
type Dispatcher interface {
	Dispatch(ctx context.Context, run func(context.Context))
}

// DispatcherFunc adapts a function to a Dispatcher.
type DispatcherFunc func(ctx context.Context, run func(context.Context))

// Dispatch calls f(ctx, run).
func (f DispatcherFunc) Dispatch(ctx context.Context, run func(context.Context)) { f(ctx, run) }

// goDispatcher is the default Dispatcher: one goroutine per dispatch.
type goDispatcher struct{}

func (goDispatcher) Dispatch(ctx context.Context, run func(context.Context)) { go run(ctx) }

// phasePolicy is how the orchestrator runs a phase's extensions. The extension author never
// chooses it: the same extension behaves as a guard in a guard phase and as a notification in a
// notify phase.
type phasePolicy int

const (
	// policyGuard runs the extensions inline, before the change they guard, and returns the first
	// error so the caller aborts. A declared extension that is not registered is an error.
	policyGuard phasePolicy = iota + 1
	// policyNotify hands the extensions to the Dispatcher, after the change they report has been
	// committed. Errors, and declared extensions that are not registered, are logged.
	policyNotify
)

var phasePolicies = map[types.ExecutionPhase]phasePolicy{
	types.PhasePreResume:  policyGuard,
	types.PhasePostResume: policyNotify,
}

// occurrence identifies the event a phase fires for: one run of one step. It is the subject of
// the extensions' idempotency key.
type occurrence struct {
	taskID string
	stepID string
	seq    int64
}

func (o occurrence) key(phase types.ExecutionPhase) string {
	return o.taskID + "/" + o.stepID + "/" + strconv.FormatInt(o.seq, 10) + "/" + string(phase)
}

// fire runs the extensions among cfgs that are declared in phase, the way phase's policy says. It
// is a no-op when the TaskManager has no extensions registry or none of cfgs is in phase.
//
// Extensions receive deep copies of record and payload, whatever the policy, so they cannot change
// what the caller persists or sends to the workflow, and a dispatched run shares no maps with the
// caller. The context they receive carries an extensions.Invocation for occ.
//
// Only a guard phase returns an error. An unknown phase is a programming error in the orchestrator
// and is returned whatever the policy would have been.
func (tm *TaskManager) fire(ctx context.Context, phase types.ExecutionPhase, occ occurrence, record store.TaskRecord, payload map[string]any, cfgs []types.ExtensionConfig) error {
	policy, known := phasePolicies[phase]
	if !known {
		return fmt.Errorf("fire extensions: unknown phase %q", phase)
	}
	if tm.extensionsRegistry == nil {
		return nil
	}
	declared := make([]types.ExtensionConfig, 0, len(cfgs))
	for _, cfg := range cfgs {
		if cfg.Phase == phase {
			declared = append(declared, cfg)
		}
	}
	if len(declared) == 0 {
		return nil
	}

	ctx = extensions.WithInvocation(ctx, extensions.Invocation{Phase: phase, IdempotencyKey: occ.key(phase)})
	record = record.DeepCopy()
	payload = deepcopy.Map(payload)

	if policy == policyGuard {
		return tm.runExtensions(ctx, &record, phase, declared, payload, true)
	}
	tm.dispatcher.Dispatch(context.WithoutCancel(ctx), func(ctx context.Context) {
		_ = tm.runExtensions(ctx, &record, phase, declared, payload, false)
	})
	return nil
}

// runExtensions executes cfgs, all declared in phase, against record in order. When stopOnError is
// true the first failure aborts and is returned; otherwise failures are logged and execution
// continues.
func (tm *TaskManager) runExtensions(ctx context.Context, record *store.TaskRecord, phase types.ExecutionPhase, cfgs []types.ExtensionConfig, payload map[string]any, stopOnError bool) error {
	for _, extCfg := range cfgs {
		ext, registered := tm.extensionsRegistry.Get(extCfg.ID)
		if !registered {
			err := fmt.Errorf("%s extension %q configured but not registered", phase, extCfg.ID)
			if stopOnError {
				return err
			}
			tm.logger.ErrorContext(ctx, "extension not registered", "phase", phase, "extension_id", extCfg.ID)
			continue
		}
		if err := ext.Execute(ctx, record, payload, extCfg.Properties); err != nil {
			err = fmt.Errorf("%s extension %q failed: %w", phase, extCfg.ID, err)
			if stopOnError {
				return err
			}
			tm.logger.ErrorContext(ctx, "extension execution failed", "phase", phase, "extension_id", extCfg.ID, "error", err)
		}
	}
	return nil
}
