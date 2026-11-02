// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktemplate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/types"
)

// Kind is owned here.
const Kind artifact.Kind = "task_template"

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
	if err := checkPolicyNulls(raw); err != nil {
		return fmt.Errorf("task template %q: %w", t.ID, err)
	}
	for _, role := range t.Read.Roles {
		if role == "" {
			return fmt.Errorf("task template %q: read.roles has an empty or null role", t.ID)
		}
	}
	l.TaskTemplate = t
	return nil
}

// checkPolicyNulls refuses an explicit null for claimable, read or read.roles. encoding/json
// decodes each as its zero value (no claims, no read roles), so a null would pass for a value
// it never states. Leaving a field out is still allowed.
func checkPolicyNulls(raw []byte) error {
	var fields struct {
		Claimable json.RawMessage `json:"claimable"`
		Read      json.RawMessage `json:"read"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if isNull(fields.Claimable) {
		return fmt.Errorf("claimable is null; leave it out or give true or false")
	}
	if fields.Read == nil {
		return nil
	}
	if isNull(fields.Read) {
		return fmt.Errorf("read is null; leave it out or give an object")
	}
	var read struct {
		Roles json.RawMessage `json:"roles"`
	}
	if err := json.Unmarshal(fields.Read, &read); err != nil {
		return err
	}
	if isNull(read.Roles) {
		return fmt.Errorf("read.roles is null; leave it out or give a list")
	}
	return nil
}

// isNull reports whether v, a field's raw JSON, is the literal null. A field left out is nil,
// not null.
func isNull(v json.RawMessage) bool {
	return string(v) == "null"
}

func Load(ctx context.Context, reg *artifact.Registry, id string) (types.TaskTemplate, error) {
	w, err := artifact.Latest[loadable](ctx, reg, id)
	return w.TaskTemplate, err
}
