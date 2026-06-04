package subtasktemplate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
	"github.com/OpenNSW/core/artifactadapter/subtasktemplate"
)

func TestSubTaskTemplateAdapter(t *testing.T) {
	t.Run("Load returns unwrapped subtask template", func(t *testing.T) {
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"subtask_v1.json": []byte(`{"id": "test_subtask", "task_type": "USER_INPUT", "plugin_properties": {"form_id": "form_1"}, "output_namespace": "out"}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("test_subtask", "subtask_template", "", "mem", "subtask_v1.json")

		template, err := subtasktemplate.Load(context.Background(), reg, "test_subtask")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if template.ID != "test_subtask" {
			t.Errorf("expected ID 'test_subtask', got %q", template.ID)
		}
		if template.TaskType != "USER_INPUT" {
			t.Errorf("expected TaskType 'USER_INPUT', got %q", template.TaskType)
		}
		if template.OutputNamespace != "out" {
			t.Errorf("expected OutputNamespace 'out', got %q", template.OutputNamespace)
		}
	})

	t.Run("Load missing returns ErrNotFound", func(t *testing.T) {
		reg := artifact.NewRegistry()
		_, err := subtasktemplate.Load(context.Background(), reg, "missing")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}
