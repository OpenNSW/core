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

// StateView declares what affordances a task offers while it sits in a given
// state. Empty (or missing) means the state is terminal / non-interactive.
type StateView struct {
	Actions []Action `json:"actions,omitempty"`
}

// TaskTemplateConfig is the trader-app-level view of a render.json blob. The
// projector fields (templateId, dataKey, visibleWhen, …) are decoded by
// uiprojector and not modeled here. Only Sections (role + handles) and
// States (lifecycle action list) are consumed by the assembler.
type TaskTemplateConfig struct {
	Sections map[string]SectionView `json:"sections,omitempty"`
	States   map[string]StateView   `json:"states,omitempty"`
}

// EnrichedComponent is the per-zone wire shape after the assembler merges
// projector output (Type, Payload) with the section's handles legal in the
// current state. There is no separate role/interactivity label: a zone is
// interactive iff Handles is non-empty, which is the only fact any consumer
// needs to decide editability and footer visibility.
type EnrichedComponent struct {
	Type    string          `json:"type"`
	Handles []HandleClaim   `json:"handles,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

// ZoneView is the wire shape the trader-app's zone renderer consumes (see
// portals/apps/trader-app/src/zones/types.ts). View is the merged per-zone
// map (slot → EnrichedComponent) emitted by the assembler; no separate
// top-level actions list — actions ship inside their claiming zone's handles.
type ZoneView struct {
	TaskID    string          `json:"task_id"`
	TaskType  string          `json:"task_type"`
	State     string          `json:"state"`
	View      json.RawMessage `json:"view"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}
