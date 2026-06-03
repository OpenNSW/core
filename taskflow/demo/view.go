package main

import "encoding/json"

// UIComponent represents a single self-contained UI widget to be displayed.
type UIComponent struct {
	Type    string          `json:"type"`    // e.g., "markdown", "jsonforms", "data_table"
	Payload json.RawMessage `json:"payload"` // The specific data the widget needs to render
}

// RenderResult maps conceptual UI slots (e.g., "primary_action", "sidebar_help")
// to their respective components. It is the view shape produced by SimpleRenderer;
// the core Renderer interface itself is agnostic about view shape.
type RenderResult map[string]UIComponent
