// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package renderer

import (
	"context"
	"encoding/json"
)

// Facts is everything a Renderer is told about the task being rendered.
type Facts struct {
	State string
	Data  map[string]any
	// Claims carries authorization decisions the caller resolved before
	// rendering, keyed by a name the render config refers to. taskflow makes no
	// policy decision of its own — it only forwards this to the renderer, which
	// may use it to decide what the caller is allowed to see. Nil is valid and
	// means "the caller resolved nothing"; a renderer that requires a claim it
	// was not given is expected to fail loudly rather than silently hide
	// content.
	Claims map[string]bool
}

// Renderer is the domain-driven engine that generates the UI view from task state and config.
// The returned bytes are passed through verbatim to the frontend; the core makes no assumptions
// about the view shape. See demo/SimpleRenderer for one convention (slot → component).
type Renderer interface {
	// Render takes the persistent render configuration snapshot and the current task state
	// (data, status, etc.) to produce the final frontend view.
	Render(ctx context.Context, config json.RawMessage, facts Facts) (json.RawMessage, error)
}
