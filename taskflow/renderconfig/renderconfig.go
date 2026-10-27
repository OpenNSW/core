// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package renderconfig resolves the render config a task runs with. A task record names its task
// template by id; the template names the render config, and both are artifacts read through the
// manifest version the task runs on, so the config stays the one the task started with whatever is
// published later.
package renderconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/generictemplate"
	"github.com/OpenNSW/core/artifact/adapter/tasktemplate"
	"github.com/OpenNSW/core/taskflow/store"
)

// ErrNoTaskTemplate means the task record names no task template.
var ErrNoTaskTemplate = errors.New("task has no task template")

// Load returns record's render config: the generic template that task template
// record.TaskTemplateID names, both in manifest version record.ManifestVersion.
func Load(ctx context.Context, artifacts *artifact.Set, record store.TaskRecord) (json.RawMessage, error) {
	if record.TaskTemplateID == "" {
		return nil, fmt.Errorf("task %s: %w", record.TaskID, ErrNoTaskTemplate)
	}
	reg, err := artifacts.Registry(ctx, record.ManifestVersion)
	if err != nil {
		return nil, fmt.Errorf("task %s: task template %q: %w", record.TaskID, record.TaskTemplateID, err)
	}
	template, err := tasktemplate.Load(ctx, reg, record.TaskTemplateID)
	if err != nil {
		return nil, fmt.Errorf("task %s: task template %q in manifest version %d: %w", record.TaskID, record.TaskTemplateID, record.ManifestVersion, err)
	}
	raw, err := generictemplate.Load(ctx, reg, template.RenderConfigID)
	if err != nil {
		return nil, fmt.Errorf("task %s: render config %q of task template %q in manifest version %d: %w", record.TaskID, template.RenderConfigID, template.ID, record.ManifestVersion, err)
	}
	return raw, nil
}
