package generictemplate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/artifact"
)

// Kind is owned here.
const Kind artifact.Kind = "generic_template"

type loadable struct {
	json.RawMessage
}

func (loadable) Kind() artifact.Kind { return Kind }

func (l *loadable) Parse(raw []byte) error {
	if !json.Valid(raw) {
		return fmt.Errorf("invalid json")
	}
	l.RawMessage = raw
	return nil
}

func Load(ctx context.Context, reg *artifact.Registry, id string) (json.RawMessage, error) {
	w, err := artifact.Latest[loadable](ctx, reg, id)
	return w.RawMessage, err
}
