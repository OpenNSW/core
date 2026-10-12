// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package generictemplate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/kinds"
)

// Kind is owned here.
const Kind = kinds.GenericTemplate

type loadable struct {
	json.RawMessage
}

func (loadable) Kind() artifact.Kind { return Kind }

func (l *loadable) Parse(raw []byte) error {
	if !json.Valid(raw) {
		return fmt.Errorf("invalid json")
	}
	l.RawMessage = raw
	return nil
}

func Load(ctx context.Context, reg *artifact.Registry, id string) (json.RawMessage, error) {
	w, err := artifact.Latest[loadable](ctx, reg, id)
	return w.RawMessage, err
}

// Check parses a generic template. Its content is free-form JSON, except that a render config
// (a uiprojector blueprint: a top-level "sections" object) names the template each section
// renders, and the renderer fetches every one; those are returned as references to generic
// templates.
func Check(raw []byte) ([]artifact.Ref, error) {
	var t loadable
	if err := t.Parse(raw); err != nil {
		return nil, err
	}
	var blueprint struct {
		Sections map[string]json.RawMessage `json:"sections"`
	}
	// Not an object, or no sections object: not a render config.
	if json.Unmarshal(raw, &blueprint) != nil || blueprint.Sections == nil {
		return nil, nil
	}
	zones := make([]string, 0, len(blueprint.Sections))
	for zone := range blueprint.Sections {
		zones = append(zones, zone)
	}
	sort.Strings(zones)
	refs := make([]artifact.Ref, 0, len(zones))
	for _, zone := range zones {
		var section struct {
			TemplateID string `json:"templateId"`
		}
		if err := json.Unmarshal(blueprint.Sections[zone], &section); err != nil {
			return nil, fmt.Errorf("render config section %q: %w", zone, err)
		}
		if section.TemplateID == "" {
			return nil, fmt.Errorf("render config section %q has no templateId", zone)
		}
		refs = append(refs, artifact.Ref{ID: section.TemplateID, Kinds: []artifact.Kind{Kind}, Field: fmt.Sprintf("section %q templateId", zone)})
	}
	return refs, nil
}
