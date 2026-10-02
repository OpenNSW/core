// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package steptemplate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/steptemplate"
	"github.com/OpenNSW/core/artifact/testutil"
)

func TestStepTemplateAdapter(t *testing.T) {
	t.Run("Load returns unwrapped step template", func(t *testing.T) {
		m := testutil.MemLoader{
			"subtask_v1.json": []byte(`{"id": "test_step", "task_type": "USER_INPUT", "plugin_properties": {"form_id": "form_1"}, "output_namespace": "out"}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("test_step", "subtask_template", "", "subtask_v1.json")

		template, err := steptemplate.Load(context.Background(), reg, "test_step")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if template.ID != "test_step" {
			t.Errorf("expected ID 'test_step', got %q", template.ID)
		}
		if template.PluginType != "USER_INPUT" {
			t.Errorf("expected PluginType 'USER_INPUT', got %q", template.PluginType)
		}
		if template.OutputNamespace != "out" {
			t.Errorf("expected OutputNamespace 'out', got %q", template.OutputNamespace)
		}
	})

	t.Run("Load missing returns ErrNotFound", func(t *testing.T) {
		reg := artifact.NewRegistry(testutil.MemLoader{})
		_, err := steptemplate.Load(context.Background(), reg, "missing")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}
