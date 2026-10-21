// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import "log/slog"

// Option configures a TaskManager.
type Option func(*TaskManager)

// WithLogger overrides the structured logger used by TaskManager.
// Defaults to slog.Default() when not provided.
func WithLogger(logger *slog.Logger) Option {
	return func(tm *TaskManager) {
		if logger != nil {
			tm.logger = logger
		}
	}
}

// WithRequiredPreResumeExtensions makes CompleteTaskStep and SaveDraft refuse, with
// ErrStepNotGuarded, a step whose template does not declare every one of ids in its PRE_RESUME
// phase, or any step at all when the TaskManager has no extensions registry to run them.
//
// Without it a step's PRE_RESUME extensions are opt-in: a template that declares none resumes for
// any caller who reaches these methods. Pass the id of the extension that authorizes callers (for
// example "authz") so that a template which forgets it fails closed instead.
//
// Empty ids are ignored. A later call replaces the ids an earlier one set.
func WithRequiredPreResumeExtensions(ids ...string) Option {
	return func(tm *TaskManager) {
		required := make([]string, 0, len(ids))
		for _, id := range ids {
			if id != "" {
				required = append(required, id)
			}
		}
		tm.requiredPreResume = required
	}
}
