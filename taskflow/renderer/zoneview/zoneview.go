// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package zoneview

import (
	"encoding/json"
	"time"
)

// Action is the state-level operational record: in this state, this command
// is legal. Command is the sole identifier; dispatch behavior (whether to
// gather form data, validation gating, etc.) is decided by the claiming
// section's renderer based on its element catalog — not by the action.
type Action struct {
	Command string `json:"command"`
}

// HandleClaim is one section-side binding of a command to a renderer-defined
// screen element. Command joins this claim to a state-level Action; Label is
// the user-facing text; Element is a free-form identifier owned by the
// section's renderer (e.g. "primary_action", "secondary_action" for a FORM
// projector) — the data layer does not interpret it.
//
// "Claim" here means a section laying claim to a command. It is unrelated to
// the authorization claims in renderer.Facts.Claims, which decide whether a
// section is rendered at all.
type HandleClaim struct {
	Command string `json:"command"`
	Label   string `json:"label"`
	Element string `json:"element,omitempty"`
}

// SectionView is the per-zone trader-app metadata read from render.json
// alongside uiprojector's Blueprint. Handles enumerates the commands the
// section claims; role is derived from those during merge (interactive iff
// any claimed handle is legal in the current state). uiprojector ignores
// this field entirely.
type SectionView struct {
	Handles []HandleClaim `json:"handles,omitempty"`
}

// LayoutRef points a state at one of the render config's named layouts. Ref is
// a JSON pointer of the form "#/layouts/<name>".
type LayoutRef struct {
	Ref string `json:"$ref"`
}

// StateView declares what affordances a task offers while it sits in a given
// state. Empty (or missing) means the state is terminal / non-interactive.
// Order selects the layout that fixes the render order of the sections
// visible in this state; without it, sections are ordered by their key.
type StateView struct {
	Actions []Action   `json:"actions,omitempty"`
	Order   *LayoutRef `json:"order,omitempty"`
}

// TaskTemplateConfig is the trader-app-level view of a render.json blob. The
// projector fields (templateId, dataKey, visibleWhen, …) are decoded by
// uiprojector and not modeled here. Only Sections (handles), Layouts and
// States (lifecycle action list + layout reference) are consumed here.
//
// Each layout is a permutation of all section keys and expresses relative
// order only, never visibility: visibility stays with each section's
// visibleWhen. Several states may share one layout when they agree on the
// relative order of the sections they show.
type TaskTemplateConfig struct {
	Sections map[string]SectionView `json:"sections,omitempty"`
	Layouts  map[string][]string    `json:"layouts,omitempty"`
	States   map[string]StateView   `json:"states,omitempty"`
}

// EnrichedComponent is one entry of the rendered view: projector output
// (Title, Type, Payload) merged with the section's handles legal in the
// current state. The section key joins the two and is then dropped; the
// entry's position in the view is its render order. There is no separate
// role/interactivity label: a zone is interactive iff Handles is non-empty,
// which is the only fact any consumer needs to decide editability and footer
// visibility.
type EnrichedComponent struct {
	Title   string          `json:"title,omitempty"`
	Type    string          `json:"type"`
	Handles []HandleClaim   `json:"handles,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

// ZoneView is the wire shape the trader-app's zone renderer consumes (see
// portals/apps/trader-app/src/zones/types.ts). View is the ordered list of
// EnrichedComponent emitted by TaskRenderer; no separate top-level actions
// list — actions ship inside their claiming zone's handles.
type ZoneView struct {
	TaskID    string          `json:"task_id"`
	TaskType  string          `json:"task_type"`
	State     string          `json:"state"`
	View      json.RawMessage `json:"view"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}
