package tasktemplate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/taskflow/types"
)

// Kind is owned here.
const Kind artifact.Kind = "task_template"

type loadable struct {
	types.TaskTemplate
}

func (loadable) Kind() artifact.Kind { return Kind }

func (l *loadable) Parse(raw []byte) error {
	var t types.TaskTemplate
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("decode task template: %w", err)
	}
	if t.ID == "" {
		return fmt.Errorf("task template: missing id")
	}
	l.TaskTemplate = t
	return nil
}

func Load(ctx context.Context, reg *artifact.Registry, id string) (types.TaskTemplate, error) {
	w, err := artifact.Latest[loadable](ctx, reg, id)
	return w.TaskTemplate, err
}
