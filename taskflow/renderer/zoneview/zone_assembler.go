// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package zoneview

import (
	"context"
	"fmt"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/taskflow/renderconfig"
	tfrenderer "github.com/OpenNSW/core/taskflow/renderer"
	"github.com/OpenNSW/core/taskflow/store"
)

// ZoneViewAssembler builds the ZoneView payload served by GET /api/v1/tasks/{id}.
// TaskRenderer produces the complete view — projected sections merged with
// their legal handles, in layout order — so the assembler only resolves the
// task's render config, forwards the caller's claims and wraps the view with the
// task's identity and timestamps.
type ZoneViewAssembler struct {
	inner     *TaskRenderer
	artifacts *artifact.Set // resolves each task's render config (see renderconfig.Load)
}

func NewZoneViewAssembler(inner *TaskRenderer, artifacts *artifact.Set) *ZoneViewAssembler {
	return &ZoneViewAssembler{inner: inner, artifacts: artifacts}
}

// Assemble renders record for one caller. claims are the authorization
// decisions that caller resolved beforehand (see tfrenderer.Facts.Claims): the
// assembler forwards them to the projector and makes no policy decision of its
// own. Pass nil when the render config gates nothing on a claim. Because a
// hidden section is absent from the projector's output, its handles are dropped
// with it — the renderer only decorates sections the projector actually emitted.
// Templates resolve through record.ManifestVersion, which Assemble puts on ctx.
func (a *ZoneViewAssembler) Assemble(ctx context.Context, record store.TaskRecord, claims map[string]bool) (ZoneView, error) {
	// The template provider resolves the render config's templates through the task's manifest
	// version (artifact.ManifestVersionFrom), so a form does not change under an open task.
	renderConfig, err := renderconfig.Load(ctx, a.artifacts, record)
	if err != nil {
		return ZoneView{}, fmt.Errorf("zone assembler: %w", err)
	}
	ctx = artifact.WithManifestVersion(ctx, record.ManifestVersion)
	view, err := a.inner.Render(ctx, renderConfig, tfrenderer.Facts{
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
		StepID:    record.ActiveStepID,
		Version:   record.Seq,
		ClaimedBy: record.ClaimedBy,
		ClaimedAt: record.ClaimedAt,
		View:      view,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}, nil
}
