package tasktemplate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
	"github.com/OpenNSW/core/artifactadapter/tasktemplate"
)

func TestTaskTemplateAdapter(t *testing.T) {
	t.Run("Load returns unwrapped task template", func(t *testing.T) {
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"task_v1.json": []byte(`{"id": "test_task", "type": "APPLICATION", "workflow_id": "test_wf", "render_config_id": "test_cfg"}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("test_task", "task_template", "", "mem", "task_v1.json")

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

	t.Run("Load missing returns ErrNotFound", func(t *testing.T) {
		reg := artifact.NewRegistry()
		_, err := tasktemplate.Load(context.Background(), reg, "missing")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}
