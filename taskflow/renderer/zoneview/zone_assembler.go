// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package zoneview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	tfrenderer "github.com/OpenNSW/core/taskflow/renderer"
	"github.com/OpenNSW/core/taskflow/store"
)

// ZoneViewAssembler builds the ZoneView payload served by GET /api/v1/tasks/{id}.
// It calls TaskRenderer for the projector-driven view bytes, then merges the
// trader-app layering — section role/handles + state-level action legality —
// into a single per-zone wire object. The render config is decoded twice
// (once by uiprojector inside TaskRenderer, once here for Sections/States);
// each parser ignores fields it doesn't own.
type ZoneViewAssembler struct {
	inner *TaskRenderer
}

func NewZoneViewAssembler(inner *TaskRenderer) *ZoneViewAssembler {
	return &ZoneViewAssembler{inner: inner}
}

// Assemble renders record for one caller. claims are the authorization
// decisions that caller resolved beforehand (see tfrenderer.Facts.Claims): the
// assembler forwards them to the projector and makes no policy decision of its
// own. Pass nil when the render config gates nothing on a claim. Because a
// hidden section is absent from the projector's output, its handles are dropped
// with it — mergeView only decorates slots the projector actually emitted.
func (a *ZoneViewAssembler) Assemble(ctx context.Context, record store.TaskRecord, claims map[string]bool) (ZoneView, error) {
	viewBytes, err := a.inner.Render(ctx, record.RenderConfig, tfrenderer.Facts{
		State:  record.State,
		Data:   record.Data,
		Claims: claims,
	})
	if err != nil {
		return ZoneView{}, fmt.Errorf("zone assembler: render: %w", err)
	}

	var cfg TaskTemplateConfig
	if len(record.RenderConfig) > 0 {
		if err := json.Unmarshal(record.RenderConfig, &cfg); err != nil {
			return ZoneView{}, fmt.Errorf("zone assembler: decode trader-app layering: %w", err)
		}
	}

	merged, err := mergeView(viewBytes, cfg, record.State, sectionKeyOrder(record.RenderConfig))
	if err != nil {
		return ZoneView{}, fmt.Errorf("zone assembler: merge: %w", err)
	}

	return ZoneView{
		TaskID:    record.TaskID,
		TaskType:  record.TaskType,
		State:     record.State,
		View:      merged,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}, nil
}

// mergeView decorates each slot in the projector-produced view with the
// section's role and the subset of its handles whose command/action is legal
// in the current state. A slot present in the view but missing from
// cfg.Sections renders with empty role and no handles; a section's handle
// whose identifier doesn't appear in states[currentState].actions is
// dropped.
//
// sectionOrder is the key order of render.json's sections object. encoding/json
// sorts map keys alphabetically, which would put review_history above
// status_awaiting even when the config listed Current Status first. The
// trader-app renders unknown slots in JSON key order, so the wire object must
// follow the config.
func mergeView(viewBytes json.RawMessage, cfg TaskTemplateConfig, state string, sectionOrder []string) (json.RawMessage, error) {
	if len(viewBytes) == 0 {
		return json.RawMessage("{}"), nil
	}

	// Decode renderer output into a generic map so we can preserve Type and
	// Payload without re-typing per-projector payload shapes.
	var raw map[string]struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(viewBytes, &raw); err != nil {
		return nil, fmt.Errorf("decode view: %w", err)
	}

	legal := legalCommands(cfg.States[state].Actions)
	out := make(map[string]EnrichedComponent, len(raw))
	for slot, comp := range raw {
		sec := cfg.Sections[slot]
		out[slot] = EnrichedComponent{
			Type:    comp.Type,
			Handles: filterLegalHandles(sec.Handles, legal),
			Payload: comp.Payload,
		}
	}

	merged, err := marshalViewInOrder(sectionOrder, out)
	if err != nil {
		return nil, fmt.Errorf("marshal merged view: %w", err)
	}
	return merged, nil
}

// sectionKeyOrder returns the sections object keys in render.json document
// order. A missing or non-object sections field yields a nil slice, which
// marshalViewInOrder treats as "sorted leftovers only" — the historical
// encoding/json map behaviour.
func sectionKeyOrder(renderConfig json.RawMessage) []string {
	if len(renderConfig) == 0 {
		return nil
	}
	var envelope struct {
		Sections json.RawMessage `json:"sections"`
	}
	if err := json.Unmarshal(renderConfig, &envelope); err != nil || len(envelope.Sections) == 0 {
		return nil
	}
	return jsonObjectKeys(envelope.Sections)
}

func jsonObjectKeys(raw json.RawMessage) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return nil
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return keys
		}
		key, ok := tok.(string)
		if !ok {
			return keys
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}

func marshalViewInOrder(order []string, values map[string]EnrichedComponent) (json.RawMessage, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	write := func(k string, v EnrichedComponent) error {
		kb, err := json.Marshal(k)
		if err != nil {
			return err
		}
		vb, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	for _, k := range order {
		v, ok := values[k]
		if !ok {
			continue
		}
		if err := write(k, v); err != nil {
			return nil, err
		}
		seen[k] = struct{}{}
	}
	if len(seen) != len(values) {
		extra := make([]string, 0, len(values)-len(seen))
		for k := range values {
			if _, ok := seen[k]; !ok {
				extra = append(extra, k)
			}
		}
		sort.Strings(extra)
		for _, k := range extra {
			if err := write(k, values[k]); err != nil {
				return nil, err
			}
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
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
