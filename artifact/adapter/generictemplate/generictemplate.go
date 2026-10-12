// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package generictemplate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/kinds"
)

// Kind is owned here.
const Kind = kinds.GenericTemplate

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

// Check parses a generic template. Its content is free-form JSON, so no references are returned.
func Check(raw []byte) ([]artifact.Ref, error) {
	var t loadable
	return nil, t.Parse(raw)
}
