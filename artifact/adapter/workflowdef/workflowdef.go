// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package workflowdef

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/kinds"
	engine "github.com/OpenNSW/core/workflow"
)

// Kind is owned here — the type that confers "artifact-ness" on a workflow def.
const Kind = kinds.Workflow

// loadable is the adapter: a local type (so we may legally define methods on it)
// that embeds the pure DSL type and satisfies artifact.Artifact + artifact.Parser.
// Unexported — callers use Load and never see it.
type loadable struct {
	engine.WorkflowDefinition
}

func (loadable) Kind() artifact.Kind { return Kind }

func (w *loadable) Parse(raw []byte) error {
	if err := json.Unmarshal(raw, w); err != nil {
		return fmt.Errorf("decode workflow definition: %w", err)
	}
	if w.ID == "" {
		return fmt.Errorf("workflow definition: missing id")
	}
	return nil
}

// Load returns a workflow definition from reg, which is one manifest version's registry. Callers
// get a plain engine.WorkflowDefinition back — they never see the adapter or know "artifact" was
// involved in fetching it.
func Load(ctx context.Context, reg *artifact.Registry, id string) (engine.WorkflowDefinition, error) {
	w, err := artifact.Latest[loadable](ctx, reg, id)
	return w.WorkflowDefinition, err
}

// Check parses a workflow definition and returns what its nodes point at: a TASK node's
// task_template_id names a task template (in a case's top-level workflow) or a step template (in
// a task's own workflow), and a SAME_TEMPLATE SPLIT_TASK node's names the workflow every branch
// runs. A DIFFERENT_TEMPLATES split takes each branch's workflow from its items at run time,
// which no static check can see, and ignores the node's own task_template_id.
func Check(raw []byte) ([]artifact.Ref, error) {
	var w loadable
	if err := w.Parse(raw); err != nil {
		return nil, err
	}
	var refs []artifact.Ref
	for _, n := range w.Nodes {
		switch n.Type {
		case engine.NodeTypeTask:
			if n.TaskTemplateID == "" {
				return nil, fmt.Errorf("workflow %q: TASK node %q has no task_template_id", w.ID, n.ID)
			}
			refs = append(refs, artifact.Ref{
				ID:    n.TaskTemplateID,
				Kinds: []artifact.Kind{kinds.TaskTemplate, kinds.StepTemplate},
				Field: fmt.Sprintf("node %q task_template_id", n.ID),
			})
		case engine.NodeTypeSplitTask:
			if n.SplitTask != nil && n.SplitTask.Mode == engine.SplitModeDifferentTemplates {
				continue
			}
			// The engine runs node.task_template_id for every branch of any other split.
			if n.TaskTemplateID == "" {
				return nil, fmt.Errorf("workflow %q: SPLIT_TASK node %q runs one template for every branch but has no task_template_id", w.ID, n.ID)
			}
			refs = append(refs, artifact.Ref{
				ID:    n.TaskTemplateID,
				Kinds: []artifact.Kind{Kind},
				Field: fmt.Sprintf("node %q task_template_id", n.ID),
			})
		}
	}
	return refs, nil
}
