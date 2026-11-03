// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktemplate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/tasktemplate"
	"github.com/OpenNSW/core/artifact/testutil"
)

func TestTaskTemplateAdapter(t *testing.T) {
	t.Run("Load returns unwrapped task template", func(t *testing.T) {
		m := testutil.MemLoader{
			"task_v1.json": []byte(`{"id": "test_task", "type": "APPLICATION", "workflow_id": "test_wf", "render_config_id": "test_cfg"}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("test_task", "task_template", "", "task_v1.json")

		template, err := tasktemplate.Load(context.Background(), reg, "test_task")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if template.ID != "test_task" {
			t.Errorf("expected ID 'test_task', got %q", template.ID)
		}
		if template.Type != "APPLICATION" {
			t.Errorf("expected Type 'APPLICATION', got %q", template.Type)
		}
	})

	t.Run("Load returns claimable and read roles", func(t *testing.T) {
		m := testutil.MemLoader{
			"task_v1.json": []byte(`{"id": "test_task", "type": "APPLICATION", "workflow_id": "test_wf", "render_config_id": "test_cfg",
				"claimable": true, "read": {"roles": ["officer", "supervisor"]}}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("test_task", "task_template", "", "task_v1.json")

		template, err := tasktemplate.Load(context.Background(), reg, "test_task")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !template.Claimable {
			t.Error("expected Claimable")
		}
		if got := template.Read.Roles; len(got) != 2 || got[0] != "officer" || got[1] != "supervisor" {
			t.Errorf("expected read roles [officer supervisor] in order, got %v", got)
		}
	})

	t.Run("Load without claimable or read leaves them empty", func(t *testing.T) {
		m := testutil.MemLoader{
			"task_v1.json": []byte(`{"id": "test_task", "type": "APPLICATION", "workflow_id": "test_wf", "render_config_id": "test_cfg"}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("test_task", "task_template", "", "task_v1.json")

		template, err := tasktemplate.Load(context.Background(), reg, "test_task")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if template.Claimable || len(template.Read.Roles) != 0 {
			t.Errorf("expected no claims and no read roles, got claimable=%v roles=%v", template.Claimable, template.Read.Roles)
		}
	})

	// claimable is a boolean: a string or number is refused, so "true" never counts as true.
	t.Run("Load refuses a claimable that is not a boolean", func(t *testing.T) {
		m := testutil.MemLoader{
			"task_v1.json": []byte(`{"id": "test_task", "type": "APPLICATION", "workflow_id": "test_wf", "render_config_id": "test_cfg", "claimable": "true"}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("test_task", "task_template", "", "task_v1.json")

		if _, err := tasktemplate.Load(context.Background(), reg, "test_task"); err == nil {
			t.Error("expected an error for a string claimable")
		}
	})

	// A role is a non-empty name: a null role decodes as "", which no catalog can define.
	for name, roles := range map[string]string{
		"a null role":   `["officer", null]`,
		"an empty role": `[""]`,
	} {
		t.Run("Load refuses "+name, func(t *testing.T) {
			m := testutil.MemLoader{
				"task_v1.json": []byte(`{"id": "test_task", "type": "APPLICATION", "workflow_id": "test_wf", "render_config_id": "test_cfg", "read": {"roles": ` + roles + `}}`),
			}
			reg := artifact.NewRegistry(m)
			reg.RegisterArtifact("test_task", "task_template", "", "task_v1.json")

			if _, err := tasktemplate.Load(context.Background(), reg, "test_task"); err == nil {
				t.Errorf("expected an error for %s", name)
			}
		})
	}

	// An empty list is accepted: the host decides alone.
	t.Run("Load accepts an empty read.roles", func(t *testing.T) {
		m := testutil.MemLoader{
			"task_v1.json": []byte(`{"id": "test_task", "type": "APPLICATION", "workflow_id": "test_wf", "render_config_id": "test_cfg", "read": {"roles": []}}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("test_task", "task_template", "", "task_v1.json")

		template, err := tasktemplate.Load(context.Background(), reg, "test_task")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(template.Read.Roles) != 0 {
			t.Errorf("expected no read roles, got %v", template.Read.Roles)
		}
	})

	t.Run("Load missing returns ErrNotFound", func(t *testing.T) {
		reg := artifact.NewRegistry(testutil.MemLoader{})
		_, err := tasktemplate.Load(context.Background(), reg, "missing")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}
