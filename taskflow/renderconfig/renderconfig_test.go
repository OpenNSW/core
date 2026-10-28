// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package renderconfig_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
	"github.com/OpenNSW/core/taskflow/renderconfig"
	"github.com/OpenNSW/core/taskflow/store"
)

// twoVersions is a source where manifest v2's task template "apply" names a new render config,
// and v1 also lists a task template whose render config it does not list.
func twoVersions(t *testing.T) *artifact.Set {
	t.Helper()
	set, err := artifact.LoadSet(context.Background(), testutil.MemLoader{
		"manifest_v1.json": []byte(`{"artifacts":[
			{"id":"apply","kind":"task_template","path":"task_v1.json"},
			{"id":"broken","kind":"task_template","path":"task_broken.json"},
			{"id":"apply-render","kind":"generic_template","path":"render_v1.json"}]}`),
		"manifest_v2.json": []byte(`{"artifacts":[
			{"id":"apply","kind":"task_template","path":"task_v2.json"},
			{"id":"apply-render-2","kind":"generic_template","path":"render_v2.json"}]}`),
		"task_v1.json":     []byte(`{"id":"apply","workflow_id":"apply-flow","render_config_id":"apply-render"}`),
		"task_v2.json":     []byte(`{"id":"apply","workflow_id":"apply-flow","render_config_id":"apply-render-2"}`),
		"task_broken.json": []byte(`{"id":"broken","workflow_id":"apply-flow","render_config_id":"missing"}`),
		"render_v1.json":   []byte(`{"title":"v1"}`),
		"render_v2.json":   []byte(`{"title":"v2"}`),
	})
	if err != nil {
		t.Fatalf("LoadSet: %v", err)
	}
	return set
}

func TestLoad(t *testing.T) {
	ctx := context.Background()
	set := twoVersions(t)

	t.Run("resolves the task template and its render config through the task's manifest version", func(t *testing.T) {
		for v, want := range map[int]string{1: `{"title":"v1"}`, 2: `{"title":"v2"}`} {
			got, err := renderconfig.Load(ctx, set, store.TaskRecord{TaskID: "t", TaskTemplateID: "apply", ManifestVersion: v})
			if err != nil {
				t.Fatalf("v%d: %v", v, err)
			}
			if string(got) != want {
				t.Errorf("v%d: got %s, want %s", v, got, want)
			}
		}
	})

	t.Run("a record without a task template id is refused", func(t *testing.T) {
		_, err := renderconfig.Load(ctx, set, store.TaskRecord{TaskID: "t", ManifestVersion: 1})
		if !errors.Is(err, renderconfig.ErrNoTaskTemplate) {
			t.Errorf("err = %v, want %v", err, renderconfig.ErrNoTaskTemplate)
		}
	})

	t.Run("a record without a manifest version is refused", func(t *testing.T) {
		_, err := renderconfig.Load(ctx, set, store.TaskRecord{TaskID: "t", TaskTemplateID: "apply"})
		if !errors.Is(err, artifact.ErrUnknownManifestVersion) {
			t.Errorf("err = %v, want %v", err, artifact.ErrUnknownManifestVersion)
		}
	})

	t.Run("a task template the manifest does not list is not found", func(t *testing.T) {
		_, err := renderconfig.Load(ctx, set, store.TaskRecord{TaskID: "t", TaskTemplateID: "missing", ManifestVersion: 1})
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("err = %v, want %v", err, artifact.ErrNotFound)
		}
	})

	t.Run("a render config the manifest does not list is not found", func(t *testing.T) {
		_, err := renderconfig.Load(ctx, set, store.TaskRecord{TaskID: "t", TaskTemplateID: "broken", ManifestVersion: 1})
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("err = %v, want %v", err, artifact.ErrNotFound)
		}
	})
}
