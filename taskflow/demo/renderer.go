package main

import (
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/nsw-task-flow/renderer"
)

// SimpleRenderer is a state-keyed renderer for the demo. The render config is
// expected to be a JSON object mapping task state to a RenderResult, with an
// optional "default" key used when no entry matches the current state.
//
// Example config:
//
//	{
//	  "PENDING_USER":      {"primary": {"type": "jsonforms", "payload": {...}}},
//	  "QUEUED_EXTERNALLY": {"primary": {"type": "markdown",  "payload": "Waiting…"}},
//	  "default":           {"primary": {"type": "markdown",  "payload": "(no view)"}}
//	}
type SimpleRenderer struct{}

func (SimpleRenderer) Render(config json.RawMessage, facts renderer.Facts) (renderer.RenderResult, error) {
	if len(config) == 0 {
		return renderer.RenderResult{}, nil
	}

	var byState map[string]renderer.RenderResult
	if err := json.Unmarshal(config, &byState); err != nil {
		return nil, fmt.Errorf("parse render config: %w", err)
	}

	if result, ok := byState[facts.State]; ok {
		return result, nil
	}
	if result, ok := byState["default"]; ok {
		return result, nil
	}
	return renderer.RenderResult{}, nil
}
