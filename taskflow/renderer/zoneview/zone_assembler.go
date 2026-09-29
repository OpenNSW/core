// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package zoneview

import (
	"context"
	"fmt"

	tfrenderer "github.com/OpenNSW/core/taskflow/renderer"
	"github.com/OpenNSW/core/taskflow/store"
)

// ZoneViewAssembler builds the ZoneView payload served by GET /api/v1/tasks/{id}.
// TaskRenderer produces the complete view — projected sections merged with
// their legal handles, in layout order — so the assembler only forwards the
// caller's claims and wraps the view with the task's identity and timestamps.
type ZoneViewAssembler struct {
	inner *TaskRenderer
}

func NewZoneViewAssembler(inner *TaskRenderer) *ZoneViewAssembler {
	return &ZoneViewAssembler{inner: inner}
}

// Assemble renders record for one caller. claims are the authorization
// decisions that caller resolved beforehand (see tfrenderer.Facts.Claims): the
// assembler forwards them to the projector and makes no policy decision of its
// own. Pass nil when the render config gates nothing on a claim. Because a
// hidden section is absent from the projector's output, its handles are dropped
// with it — the renderer only decorates sections the projector actually emitted.
func (a *ZoneViewAssembler) Assemble(ctx context.Context, record store.TaskRecord, claims map[string]bool) (ZoneView, error) {
	view, err := a.inner.Render(ctx, record.RenderConfig, tfrenderer.Facts{
		State:  record.State,
		Data:   record.Data,
		Claims: claims,
	})
	if err != nil {
		return ZoneView{}, fmt.Errorf("zone assembler: render: %w", err)
	}

	return ZoneView{
		TaskID:    record.TaskID,
		TaskType:  record.TaskType,
		State:     record.State,
		View:      view,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}, nil
}
