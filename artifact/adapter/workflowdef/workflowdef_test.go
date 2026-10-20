// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package workflowdef_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/workflowdef"
	"github.com/OpenNSW/core/artifact/testutil"
)

func TestWorkflowDefAdapter(t *testing.T) {
	t.Run("Load returns unwrapped workflow definition with fields populated", func(t *testing.T) {
		m := testutil.MemLoader{
			"wf_v1.json": []byte(`{"id": "import_clearance", "name": "Import Clearance Process", "version": 1}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("import_clearance", "workflow", "", "wf_v1.json")

		def, err := workflowdef.Load(context.Background(), reg, "import_clearance")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if def.ID != "import_clearance" {
			t.Errorf("expected ID 'import_clearance', got %q", def.ID)
		}
		if def.Name != "Import Clearance Process" {
			t.Errorf("expected Name 'Import Clearance Process', got %q", def.Name)
		}
		if def.Version != 1 {
			t.Errorf("expected Version 1, got %d", def.Version)
		}
	})

	t.Run("Load returns latest version when multiple exist", func(t *testing.T) {
		m := testutil.MemLoader{
			"wf_v1.json":  []byte(`{"id": "import_clearance", "name": "Clearance V1", "version": 1}`),
			"wf_v2.json":  []byte(`{"id": "import_clearance", "name": "Clearance V2", "version": 2}`),
			"wf_v10.json": []byte(`{"id": "import_clearance", "name": "Clearance V10", "version": 10}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("import_clearance", "workflow", "v1", "wf_v1.json")
		reg.RegisterArtifact("import_clearance", "workflow", "v2", "wf_v2.json")
		reg.RegisterArtifact("import_clearance", "workflow", "v10", "wf_v10.json")

		def, err := workflowdef.Load(context.Background(), reg, "import_clearance")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if def.Version != 10 {
			t.Errorf("expected Version 10, got %d", def.Version)
		}
	})

	t.Run("Missing ID returns error", func(t *testing.T) {
		m := testutil.MemLoader{
			"wf_invalid.json": []byte(`{"name": "No ID Process"}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("import_clearance", "workflow", "", "wf_invalid.json")

		_, err := workflowdef.Load(context.Background(), reg, "import_clearance")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("Missing artifact returns ErrNotFound", func(t *testing.T) {
		reg := artifact.NewRegistry(testutil.MemLoader{})
		_, err := workflowdef.Load(context.Background(), reg, "non_existent")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestCheck(t *testing.T) {
	t.Run("returns task and split references", func(t *testing.T) {
		refs, err := workflowdef.Check([]byte(`{"id":"wf","nodes":[
			{"id":"start","type":"START"},
			{"id":"apply","type":"TASK","task_template_id":"apply-task"},
			{"id":"fan","type":"SPLIT_TASK","task_template_id":"branch-flow","split_task":{"mode":"SAME_TEMPLATE"}},
			{"id":"dyn","type":"SPLIT_TASK","task_template_id":"ignored","split_task":{"mode":"DIFFERENT_TEMPLATES"}},
			{"id":"end","type":"END"}]}`))
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if len(refs) != 2 {
			t.Fatalf("expected 2 refs, got %+v", refs)
		}
		if refs[0].ID != "apply-task" || len(refs[0].Kinds) != 2 || refs[0].Kinds[0] != "task_template" || refs[0].Kinds[1] != "subtask_template" {
			t.Errorf("unexpected TASK ref %+v", refs[0])
		}
		if refs[1].ID != "branch-flow" || len(refs[1].Kinds) != 1 || refs[1].Kinds[0] != workflowdef.Kind {
			t.Errorf("unexpected SPLIT_TASK ref %+v", refs[1])
		}
	})

	t.Run("rejects a same-template split without a template", func(t *testing.T) {
		for _, node := range []string{
			`{"id":"fan","type":"SPLIT_TASK","split_task":{"mode":"SAME_TEMPLATE"}}`,
			`{"id":"fan","type":"SPLIT_TASK"}`,
		} {
			if _, err := workflowdef.Check([]byte(`{"id":"wf","nodes":[` + node + `]}`)); err == nil {
				t.Errorf("expected an error for %s", node)
			}
		}
	})

	t.Run("rejects a TASK node without a template", func(t *testing.T) {
		_, err := workflowdef.Check([]byte(`{"id":"wf","nodes":[{"id":"apply","type":"TASK"}]}`))
		if err == nil {
			t.Fatal("expected an error for a TASK node without task_template_id")
		}
	})

	t.Run("rejects a definition without an id", func(t *testing.T) {
		if _, err := workflowdef.Check([]byte(`{"nodes":[]}`)); err == nil {
			t.Fatal("expected an error for a definition without an id")
		}
	})
}
