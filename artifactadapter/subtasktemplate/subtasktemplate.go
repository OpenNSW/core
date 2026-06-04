package subtasktemplate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/taskflow/types"
)

// Kind is owned here.
const Kind artifact.Kind = "subtask_template"

type loadable struct {
	types.SubTaskTemplate
}

func (loadable) Kind() artifact.Kind { return Kind }

func (l *loadable) Parse(raw []byte) error {
	var t types.SubTaskTemplate
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("decode subtask template: %w", err)
	}
	if t.ID == "" {
		return fmt.Errorf("subtask template: missing id")
	}
	l.SubTaskTemplate = t
	return nil
}

func Load(ctx context.Context, reg *artifact.Registry, id string) (types.SubTaskTemplate, error) {
	w, err := artifact.Latest[loadable](ctx, reg, id)
	return w.SubTaskTemplate, err
}
