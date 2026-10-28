// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/steptemplate"
	"github.com/OpenNSW/core/artifact/adapter/types"
	"github.com/OpenNSW/core/shared/deepcopy"
	"github.com/OpenNSW/core/taskflow/store"
)

// CommandVar is the system variable naming the command a call makes. A host sets it on the
// payloads it passes to CompleteTaskStep; SaveDraft sets it to the step's draft command. The
// PRE_RESUME extensions (authz) read it from there.
const CommandVar = "__command"

// ErrDraftNotEnabled is returned by SaveDraft when the active step's template declares no draft.
// A caller serving HTTP should answer it with 400.
var ErrDraftNotEnabled = errors.New("step does not accept drafts")

// SaveDraft stores payload as a draft of step stepID of the task, so the step shows it the next
// time it is rendered. The step is not completed: the workflow is not called, and the step ID and
// the task's version do not change, so a draft never makes a view stale. Only a step whose
// template declares a draft accepts one.
//
//  1. Pre-check, from the task row: stepID is the active step, and the step is showing (its state
//     is none of STARTING_STEP, ADVANCING, COMPLETED, CANCELLED, ON_HOLD).
//  2. Pre-resume extensions (authz) run on deep copies, with the step's draft command as
//     CommandVar, whatever the payload carried. Post-resume extensions never run for a draft. A
//     TaskManager built WithRequiredPreResumeExtensions first refuses, with ErrStepNotGuarded, a
//     step whose template does not declare every required one.
//  3. The payload, without system keys, replaces the step's OutputNamespace slot of the task data,
//     and nothing else. The namespace comes from the step template, never from the caller, so a
//     draft cannot write over another step's data or internal keys.
//  4. The data is written guarded by the step, the seq read in step 1 and the state. If the step
//     was submitted or the task moved on in between, nothing is written and ErrStaleStep returned.
//
// A submission that follows replaces the slot with what it carries, and the next step starts from
// its mapped inputs, so a draft never reaches the workflow.
//
// Errors: ErrStepIDRequired, ErrTaskNotFound, ErrStaleStep, ErrDraftNotEnabled, ErrStepNotGuarded,
// or an extension's error.
func (tm *TaskManager) SaveDraft(ctx context.Context, taskID, stepID string, payload map[string]any) error {
	if stepID == "" {
		return ErrStepIDRequired
	}

	record, exists := tm.db.GetTask(ctx, taskID)
	if !exists {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	if record.ActiveStepID != stepID {
		return fmt.Errorf("%w: call was for step %q, active step is %q", ErrStaleStep, stepID, record.ActiveStepID)
	}
	switch record.State {
	case store.StateStartingStep, store.StateAdvancing, store.StateCompleted, store.StateCancelled, store.StateOnHold:
		return fmt.Errorf("%w: step %q is %s", ErrStaleStep, stepID, record.State)
	}
	seq := record.Seq

	reg, err := tm.registryFor(ctx, record.ActiveTaskTemplateID, record.ManifestVersion)
	if err != nil {
		return fmt.Errorf("task %s: %w", taskID, err)
	}
	// The draft's extensions (authz) resolve through the task's version.
	ctx = artifact.WithManifestVersion(ctx, record.ManifestVersion)

	stepTemplate, err := steptemplate.Load(ctx, reg, record.ActiveTaskTemplateID)
	if err != nil {
		return fmt.Errorf("failed to load active step template %q: %w", record.ActiveTaskTemplateID, err)
	}
	// The template's parser guarantees a draft has a command and an output namespace.
	if stepTemplate.Draft == nil {
		return fmt.Errorf("%w: step template %q", ErrDraftNotEnabled, stepTemplate.ID)
	}
	if err := tm.checkRequiredPreResume(stepTemplate); err != nil {
		return err
	}

	extPayload := deepcopy.Map(payload)
	if extPayload == nil {
		extPayload = make(map[string]any)
	}
	extPayload[CommandVar] = stepTemplate.Draft.Command
	preResumeRecord := record.DeepCopy()
	if err := tm.runExtensions(ctx, &preResumeRecord, types.PhasePreResume, stepTemplate.Extensions, extPayload, true); err != nil {
		return err
	}

	data := deepcopy.Map(record.Data)
	if data == nil {
		data = make(map[string]any)
	}
	data[stepTemplate.OutputNamespace] = withoutSystemVars(payload)

	rows, err := tm.db.WriteDraft(ctx, taskID, stepID, seq, data)
	if err != nil {
		return fmt.Errorf("write draft of step %s of task %s: %w", stepID, taskID, err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: step %q was submitted or replaced while its draft was saved", ErrStaleStep, stepID)
	}
	tm.logger.InfoContext(ctx, "draft saved", "task_id", taskID, "step_id", stepID, "seq", seq)
	return nil
}
