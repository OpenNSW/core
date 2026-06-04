package workflowdef_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
	"github.com/OpenNSW/core/artifactadapter/workflowdef"
)

func TestWorkflowDefAdapter(t *testing.T) {
	t.Run("Load returns unwrapped workflow definition with fields populated", func(t *testing.T) {
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"wf_v1.json": []byte(`{"id": "import_clearance", "name": "Import Clearance Process", "version": 1}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("import_clearance", "workflow", "", "mem", "wf_v1.json")

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

	t.Run("LoadVersion returns pinned version", func(t *testing.T) {
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"wf_v3.json": []byte(`{"id": "import_clearance", "name": "Clearance V3", "version": 3}`),
			"wf_v4.json": []byte(`{"id": "import_clearance", "name": "Clearance V4", "version": 4}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("import_clearance", "workflow", "v3", "mem", "wf_v3.json")
		reg.RegisterArtifact("import_clearance", "workflow", "v4", "mem", "wf_v4.json")

		def, err := workflowdef.LoadVersion(context.Background(), reg, "import_clearance", "v3")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if def.Version != 3 {
			t.Errorf("expected Version 3, got %d", def.Version)
		}
		if def.Name != "Clearance V3" {
			t.Errorf("expected Name 'Clearance V3', got %q", def.Name)
		}
	})

	t.Run("Load returns latest version when multiple exist", func(t *testing.T) {
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"wf_v1.json":  []byte(`{"id": "import_clearance", "name": "Clearance V1", "version": 1}`),
			"wf_v2.json":  []byte(`{"id": "import_clearance", "name": "Clearance V2", "version": 2}`),
			"wf_v10.json": []byte(`{"id": "import_clearance", "name": "Clearance V10", "version": 10}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("import_clearance", "workflow", "v1", "mem", "wf_v1.json")
		reg.RegisterArtifact("import_clearance", "workflow", "v2", "mem", "wf_v2.json")
		reg.RegisterArtifact("import_clearance", "workflow", "v10", "mem", "wf_v10.json")

		def, err := workflowdef.Load(context.Background(), reg, "import_clearance")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if def.Version != 10 {
			t.Errorf("expected Version 10, got %d", def.Version)
		}
	})

	t.Run("Missing ID returns error", func(t *testing.T) {
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"wf_invalid.json": []byte(`{"name": "No ID Process"}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("import_clearance", "workflow", "", "mem", "wf_invalid.json")

		_, err := workflowdef.Load(context.Background(), reg, "import_clearance")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("Missing artifact returns ErrNotFound", func(t *testing.T) {
		reg := artifact.NewRegistry()
		_, err := workflowdef.Load(context.Background(), reg, "non_existent")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}
