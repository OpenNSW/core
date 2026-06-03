package renderer

import (
	"context"
	"encoding/json"
)

type Facts struct {
	State string
	Data  map[string]any
}

// Renderer is the domain-driven engine that generates the UI view from task state and config.
// The returned bytes are passed through verbatim to the frontend; the core makes no assumptions
// about the view shape. See demo/SimpleRenderer for one convention (slot → component).
type Renderer interface {
	// Render takes the persistent render configuration snapshot and the current task state
	// (data, status, etc.) to produce the final frontend view.
	Render(ctx context.Context, config json.RawMessage, facts Facts) (json.RawMessage, error)
}
