package zoneview

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/taskflow/renderer"
	"github.com/OpenNSW/core/uiprojector"
)

// TaskRenderer adapts uiprojector.Assembler to the taskflow renderer
// contract. The render config blob is interpreted as a uiprojector.Blueprint;
// the resulting Sections are translated into a slot→component map and
// returned as opaque JSON bytes. Section.ID and Section.Title are dropped —
// the on-wire component has no slots for them.
type TaskRenderer struct {
	assembler *uiprojector.Assembler
}

func NewTaskRenderer(assembler *uiprojector.Assembler) *TaskRenderer {
	return &TaskRenderer{assembler: assembler}
}

// uiComponent is the per-slot wire shape produced by Render. The shape is
// chosen to match the trader-app frontend's ZoneComponent union (see
// portals/apps/trader-app/src/zones/types.ts).
type uiComponent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func (r *TaskRenderer) Render(ctx context.Context, configRaw json.RawMessage, facts renderer.Facts) (json.RawMessage, error) {
	if len(configRaw) == 0 {
		return json.RawMessage("{}"), nil
	}

	var bp uiprojector.Blueprint
	if err := json.Unmarshal(configRaw, &bp); err != nil {
		return nil, fmt.Errorf("renderer: unmarshal blueprint: %w", err)
	}

	sections, err := r.assembler.Assemble(ctx, &bp, uiprojector.Facts{
		State: facts.State,
		Data:  facts.Data,
	})
	if err != nil {
		return nil, fmt.Errorf("renderer: assemble: %w", err)
	}

	result := make(map[string]uiComponent, len(sections))
	for slot, sec := range sections {
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
		result[slot] = uiComponent{Type: secType, Payload: payload}
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("renderer: marshal result: %w", err)
	}
	return out, nil
}

var _ renderer.Renderer = (*TaskRenderer)(nil)
