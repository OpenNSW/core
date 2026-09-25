// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package zoneview

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/OpenNSW/core/taskflow/renderer"
	"github.com/OpenNSW/core/uiprojector"
)

// TaskRenderer adapts uiprojector.Assembler to the taskflow renderer
// contract. The render config blob is decoded twice — as a
// uiprojector.Blueprint for projection, and as a TaskTemplateConfig for the
// trader-app layering (handles, layouts, states); each parser ignores fields
// it doesn't own. The projected sections are merged with their handles legal
// in the current state, ordered by the state's layout, and returned as an
// ordered list of EnrichedComponent. Section keys join the two decodes and
// are dropped from the output.
type TaskRenderer struct {
	assembler *uiprojector.Assembler
}

func NewTaskRenderer(assembler *uiprojector.Assembler) *TaskRenderer {
	return &TaskRenderer{assembler: assembler}
}

func (r *TaskRenderer) Render(ctx context.Context, configRaw json.RawMessage, facts renderer.Facts) (json.RawMessage, error) {
	if len(configRaw) == 0 {
		return json.RawMessage("[]"), nil
	}

	var bp uiprojector.Blueprint
	if err := json.Unmarshal(configRaw, &bp); err != nil {
		return nil, fmt.Errorf("renderer: unmarshal blueprint: %w", err)
	}
	var cfg TaskTemplateConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return nil, fmt.Errorf("renderer: unmarshal trader-app layering: %w", err)
	}

	layout, err := resolveLayout(cfg, facts.State)
	if err != nil {
		return nil, fmt.Errorf("renderer: %w", err)
	}

	sections, err := r.assembler.Assemble(ctx, &bp, uiprojector.Facts{
		State:  facts.State,
		Data:   facts.Data,
		Claims: facts.Claims,
	})
	if err != nil {
		return nil, fmt.Errorf("renderer: assemble: %w", err)
	}

	legal := legalCommands(cfg.States[facts.State].Actions)
	result := make([]EnrichedComponent, 0, len(sections))
	for _, slot := range orderSlots(sections, layout) {
		sec := sections[slot]
		content := sec.Content
		secType := string(sec.Type)
		if secType == "MARKDOWN" {
			if str, ok := sec.Content.(string); ok {
				content = map[string]any{"content": str}
			}
		}
		payload, err := json.Marshal(content)
		if err != nil {
			return nil, fmt.Errorf("renderer: marshal section %q: %w", slot, err)
		}
		result = append(result, EnrichedComponent{
			Title:   sec.Title,
			Type:    secType,
			Handles: filterLegalHandles(cfg.Sections[slot].Handles, legal),
			Payload: payload,
		})
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("renderer: marshal result: %w", err)
	}
	return out, nil
}

const layoutRefPrefix = "#/layouts/"

// resolveLayout returns the layout the given state's order references, or nil
// when the state declares no order. A reference that is malformed or names a
// layout the config doesn't define is a config error.
func resolveLayout(cfg TaskTemplateConfig, state string) ([]string, error) {
	ref := cfg.States[state].Order
	if ref == nil {
		return nil, nil
	}
	name, ok := strings.CutPrefix(ref.Ref, layoutRefPrefix)
	if !ok || name == "" {
		return nil, fmt.Errorf("state %q: order $ref %q is not of the form %q", state, ref.Ref, layoutRefPrefix+"<name>")
	}
	layout, ok := cfg.Layouts[name]
	if !ok {
		return nil, fmt.Errorf("state %q: order references undefined layout %q", state, name)
	}
	return layout, nil
}

// orderSlots returns the keys of the projected sections in render order: the
// layout's order filtered down to the sections actually projected, followed
// by any projected section the layout doesn't list, sorted by key. A nil
// layout therefore orders every section by key.
func orderSlots(sections map[string]uiprojector.Section, layout []string) []string {
	ordered := make([]string, 0, len(sections))
	placed := make(map[string]struct{}, len(sections))
	for _, slot := range layout {
		if _, ok := sections[slot]; !ok {
			continue
		}
		if _, dup := placed[slot]; dup {
			continue
		}
		ordered = append(ordered, slot)
		placed[slot] = struct{}{}
	}

	rest := make([]string, 0, len(sections)-len(ordered))
	for slot := range sections {
		if _, ok := placed[slot]; !ok {
			rest = append(rest, slot)
		}
	}
	slices.Sort(rest)
	return append(ordered, rest...)
}

// legalCommands indexes the current state's actions by Command. The set is
// used to filter handle claims: a claim survives iff its command appears
// here.
func legalCommands(actions []Action) map[string]struct{} {
	idx := make(map[string]struct{}, len(actions))
	for _, a := range actions {
		if a.Command == "" {
			continue
		}
		idx[a.Command] = struct{}{}
	}
	return idx
}

// filterLegalHandles keeps only those claims whose command is legal in the
// current state. State gating cascades through to per-zone handles.
func filterLegalHandles(claims []HandleClaim, legal map[string]struct{}) []HandleClaim {
	if len(claims) == 0 {
		return nil
	}
	out := make([]HandleClaim, 0, len(claims))
	for _, h := range claims {
		if _, ok := legal[h.Command]; !ok {
			continue
		}
		out = append(out, h)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

var _ renderer.Renderer = (*TaskRenderer)(nil)
