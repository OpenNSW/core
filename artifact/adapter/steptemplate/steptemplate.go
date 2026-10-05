// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package steptemplate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/types"
)

// Kind is owned here. Written "subtask_template" for compatibility with existing stored templates.
//
// TODO(#taskflow-guarded-writes): change the wire value to "step_template" (and migrate every stored
// template, in this repo and in hosts) once that content migration is planned.
const Kind artifact.Kind = "subtask_template"

type loadable struct {
	types.StepTemplate
}

func (loadable) Kind() artifact.Kind { return Kind }

func (l *loadable) Parse(raw []byte) error {
	var t types.StepTemplate
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("decode step template: %w", err)
	}
	if t.ID == "" {
		return fmt.Errorf("step template: missing id")
	}
	l.StepTemplate = t
	return nil
}

func Load(ctx context.Context, reg *artifact.Registry, id string) (types.StepTemplate, error) {
	w, err := artifact.Latest[loadable](ctx, reg, id)
	return w.StepTemplate, err
}
