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
