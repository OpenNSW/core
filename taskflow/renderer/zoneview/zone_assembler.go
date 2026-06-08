package zoneview

import (
	"context"
	"encoding/json"
	"fmt"

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

func (a *ZoneViewAssembler) Assemble(ctx context.Context, record store.TaskRecord) (ZoneView, error) {
	viewBytes, err := a.inner.Render(ctx, record.RenderConfig, tfrenderer.Facts{
		State: record.State,
		Data:  record.Data,
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

	merged, err := mergeView(viewBytes, cfg, record.State)
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
func mergeView(viewBytes json.RawMessage, cfg TaskTemplateConfig, state string) (json.RawMessage, error) {
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

	merged, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal merged view: %w", err)
	}
	return merged, nil
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
