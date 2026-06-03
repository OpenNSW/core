package main

import (
	"context"
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

func (SimpleRenderer) Render(_ context.Context, config json.RawMessage, facts renderer.Facts) (json.RawMessage, error) {
	if len(config) == 0 {
		return nil, nil
	}

	// First pass: keep each top-level value as a raw message so that
	// non-state metadata (e.g. "id") can coexist with state keys without
	// triggering type errors. We only resolve the entry we actually need.
	var byKey map[string]json.RawMessage
	if err := json.Unmarshal(config, &byKey); err != nil {
		return nil, fmt.Errorf("parse render config: %w", err)
	}

	raw, ok := byKey[facts.State]
	if !ok {
		raw, ok = byKey["default"]
	}
	if !ok {
		return nil, nil
	}

	var result RenderResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse render config entry %q: %w", facts.State, err)
	}
	return json.Marshal(result)
}
