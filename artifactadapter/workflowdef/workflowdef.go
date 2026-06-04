package workflowdef

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/artifact"
	engine "github.com/OpenNSW/core/workflow"
)

// Kind is owned here — the type that confers "artifact-ness" on a workflow def.
const Kind artifact.Kind = "workflow"

// loadable is the adapter: a local type (so we may legally define methods on it)
// that embeds the pure DSL type and satisfies artifact.Artifact + artifact.Parser.
// Unexported — callers use Load/LoadVersion and never see it.
type loadable struct {
	engine.WorkflowDefinition
}

func (loadable) Kind() artifact.Kind { return Kind }

func (w *loadable) Parse(raw []byte) error {
	if err := json.Unmarshal(raw, w); err != nil {
		return fmt.Errorf("decode workflow definition: %w", err)
	}
	if w.ID == "" {
		return fmt.Errorf("workflow definition: missing id")
	}
	return nil
}

// Load returns the newest version of a workflow definition. Callers get a plain
// engine.WorkflowDefinition back — they never see the adapter or know "artifact"
// was involved in fetching it.
func Load(ctx context.Context, reg *artifact.Registry, id string) (engine.WorkflowDefinition, error) {
	w, err := artifact.Latest[loadable](ctx, reg, id)
	return w.WorkflowDefinition, err
}

// LoadVersion returns a specific pinned version (e.g. to resume a running
// instance on the version it started with).
func LoadVersion(ctx context.Context, reg *artifact.Registry, id, version string) (engine.WorkflowDefinition, error) {
	w, err := artifact.Get[loadable](ctx, reg, id, version)
	return w.WorkflowDefinition, err
}
