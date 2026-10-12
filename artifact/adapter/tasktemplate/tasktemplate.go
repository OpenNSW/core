// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktemplate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/kinds"
	"github.com/OpenNSW/core/artifact/adapter/types"
)

// Kind is owned here.
const Kind = kinds.TaskTemplate

type loadable struct {
	types.TaskTemplate
}

func (loadable) Kind() artifact.Kind { return Kind }

func (l *loadable) Parse(raw []byte) error {
	var t types.TaskTemplate
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("decode task template: %w", err)
	}
	if t.ID == "" {
		return fmt.Errorf("task template: missing id")
	}
	l.TaskTemplate = t
	return nil
}

func Load(ctx context.Context, reg *artifact.Registry, id string) (types.TaskTemplate, error) {
	w, err := artifact.Latest[loadable](ctx, reg, id)
	return w.TaskTemplate, err
}

// Check parses a task template and returns the workflow it runs and its render config. Both are
// required: starting a task loads both.
func Check(raw []byte) ([]artifact.Ref, error) {
	var t loadable
	if err := t.Parse(raw); err != nil {
		return nil, err
	}
	if t.WorkflowID == "" {
		return nil, fmt.Errorf("task template %q: missing workflow_id", t.ID)
	}
	if t.RenderConfigID == "" {
		return nil, fmt.Errorf("task template %q: missing render_config_id", t.ID)
	}
	return []artifact.Ref{
		{ID: t.WorkflowID, Kinds: []artifact.Kind{kinds.Workflow}, Field: "workflow_id"},
		{ID: t.RenderConfigID, Kinds: []artifact.Kind{kinds.GenericTemplate}, Field: "render_config_id"},
	}, nil
}
