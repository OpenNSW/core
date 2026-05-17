package renderer

import (
	"encoding/json"
)

// UIComponent represents a single self-contained UI widget to be displayed.
type UIComponent struct {
	Type    string          `json:"type"`    // e.g., "markdown", "jsonforms", "data_table"
	Payload json.RawMessage `json:"payload"` // The specific data the widget needs to render
}

// RenderResult maps conceptual UI slots (e.g., "primary_action", "sidebar_help")
// to their respective components.
type RenderResult map[string]UIComponent

type Facts struct {
	State string
	Data  map[string]any
}

// Renderer is the domain-driven engine that generates the UI view from task state and config.
type Renderer interface {
	// Render takes the persistent render configuration snapshot and the current task state
	// (data, status, etc.) to produce the final frontend view.
	Render(config json.RawMessage, facts Facts) (RenderResult, error)
}
