// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/OpenNSW/core/taskflow/extensions"
	"github.com/OpenNSW/core/taskflow/store"
)

// guardFixture is wakeUpFixture's task on step template templateID, behind a TaskManager built with
// opts and, when withRegistry, an extensions registry where "validator" allows every call. It
// counts the validator's runs and the Temporal completions.
func guardFixture(t *testing.T, templateID string, withRegistry bool, opts ...Option) (*TaskManager, *safeMockTaskStore, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	db := newSafeMockTaskStore()
	wakeUpFixture(t, db)
	withTemplate(templateID)(db)

	var validated, completed atomic.Int32
	var extReg *extensions.Registry
	if withRegistry {
		extReg = extensions.NewRegistry()
		extReg.Register("validator", &mockExtension{
			executeFunc: func(context.Context, *store.TaskRecord, map[string]any, json.RawMessage) error {
				validated.Add(1)
				return nil
			},
		})
		extReg.Register("logger", &mockExtension{})
	}
	wf := &mockTemporalManager{taskDoneFunc: func(context.Context, string, string, string, map[string]any) error {
		completed.Add(1)
		return nil
	}}
	tm := NewTaskManager(db, newTestRegistry(), newTestPluginsRegistry(), extReg, wf, noopCallback, noopRenderer{}, opts...)
	return tm, db, &validated, &completed
}

func TestCompleteTaskStep_RequiredPreResumeExtensions(t *testing.T) {
	cases := []struct {
		name         string
		template     string
		withRegistry bool
		opts         []Option
		wantErr      error // nil: the step completes
	}{
		// Without the option nothing changes: a step that declares no extension still resumes.
		{name: "not required, none declared", template: "generic_user_input", withRegistry: true},
		{name: "not required, no registry", template: "generic_user_input"},
		{name: "required and declared", template: "generic_user_input_with_extensions", withRegistry: true,
			opts: []Option{WithRequiredPreResumeExtensions("validator")}},
		{name: "required, none declared", template: "generic_user_input", withRegistry: true,
			opts: []Option{WithRequiredPreResumeExtensions("validator")}, wantErr: ErrStepNotGuarded},
		// The template has "logger", but only in POST_RESUME, which runs after the step resumed.
		{name: "required, declared only after resume", template: "generic_user_input_with_extensions", withRegistry: true,
			opts: []Option{WithRequiredPreResumeExtensions("logger")}, wantErr: ErrStepNotGuarded},
		{name: "two required, one declared", template: "generic_user_input_with_extensions", withRegistry: true,
			opts: []Option{WithRequiredPreResumeExtensions("validator", "authz")}, wantErr: ErrStepNotGuarded},
		// With no registry runExtensions would skip every extension, so nothing would check the caller.
		{name: "required, no registry", template: "generic_user_input_with_extensions",
			opts: []Option{WithRequiredPreResumeExtensions("validator")}, wantErr: ErrStepNotGuarded},
		{name: "only empty ids required", template: "generic_user_input", withRegistry: true,
			opts: []Option{WithRequiredPreResumeExtensions("")}},
		// A later option replaces an earlier one.
		{name: "later option wins", template: "generic_user_input", withRegistry: true,
			opts: []Option{WithRequiredPreResumeExtensions("validator"), WithRequiredPreResumeExtensions()}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tm, db, validated, completed := guardFixture(t, c.template, c.withRegistry, c.opts...)
			before := wakeRow(t, db)

			err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"__command": "submit", "age": 20.0})

			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("err = %v, want the step to complete", err)
				}
				if completed.Load() != 1 {
					t.Error("the step was not completed in the workflow")
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if completed.Load() != 0 || validated.Load() != 0 {
				t.Errorf("a refused step ran something: completed=%d validated=%d", completed.Load(), validated.Load())
			}
			if !reflect.DeepEqual(wakeRow(t, db), before) {
				t.Error("a refused step changed the row")
			}
		})
	}
}

// A registered but undeclared extension doesn't help, and a declared but unregistered one is
// refused by runExtensions, so the check fails closed either way.
func TestCompleteTaskStep_RequiredExtensionMustBeRegistered(t *testing.T) {
	tm, db, _, completed := guardFixture(t, "generic_user_input_with_extensions", false,
		WithRequiredPreResumeExtensions("validator"))
	// A registry that does not know "validator".
	tm.extensionsRegistry = extensions.NewRegistry()
	before := wakeRow(t, db)

	err := tm.CompleteTaskStep(context.Background(), wakeTaskID, stepA, map[string]any{"__command": "submit"})

	if err == nil || completed.Load() != 0 || !reflect.DeepEqual(wakeRow(t, db), before) {
		t.Errorf("err = %v, completed = %d: a declared but unregistered extension must refuse the step", err, completed.Load())
	}
}

func TestSaveDraft_RequiredPreResumeExtensions(t *testing.T) {
	t.Run("declared", func(t *testing.T) {
		tm, db, validated, _ := guardFixture(t, "draft_user_input", true, WithRequiredPreResumeExtensions("validator"))
		if err := tm.SaveDraft(context.Background(), wakeTaskID, stepA, map[string]any{"applicant_name": "Al"}); err != nil {
			t.Fatalf("err = %v, want the draft saved", err)
		}
		if validated.Load() != 1 {
			t.Error("the required extension did not run on the draft")
		}
		if got := wakeRow(t, db).Data["userform"]; !reflect.DeepEqual(got, map[string]any{"applicant_name": "Al"}) {
			t.Errorf("userform = %v, want the draft", got)
		}
	})

	t.Run("not declared", func(t *testing.T) {
		tm, db, validated, _ := guardFixture(t, "draft_user_input", true, WithRequiredPreResumeExtensions("authz"))
		before := wakeRow(t, db)
		err := tm.SaveDraft(context.Background(), wakeTaskID, stepA, map[string]any{"applicant_name": "Al"})
		if !errors.Is(err, ErrStepNotGuarded) {
			t.Fatalf("err = %v, want ErrStepNotGuarded", err)
		}
		if validated.Load() != 0 || !reflect.DeepEqual(wakeRow(t, db), before) {
			t.Error("a refused draft ran an extension or changed the row")
		}
	})

	// A step that takes no drafts still answers ErrDraftNotEnabled, whatever it declares.
	t.Run("drafts not enabled", func(t *testing.T) {
		tm, _, _, _ := guardFixture(t, "generic_user_input", true, WithRequiredPreResumeExtensions("authz"))
		err := tm.SaveDraft(context.Background(), wakeTaskID, stepA, map[string]any{"x": 1})
		if !errors.Is(err, ErrDraftNotEnabled) {
			t.Fatalf("err = %v, want ErrDraftNotEnabled", err)
		}
	})
}
