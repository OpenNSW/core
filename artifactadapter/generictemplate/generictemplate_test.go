package generictemplate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
	"github.com/OpenNSW/core/artifactadapter/generictemplate"
)

func TestGenericTemplateAdapter(t *testing.T) {
	t.Run("Load returns unwrapped raw JSON template", func(t *testing.T) {
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"cfg_v1.json": []byte(`{"theme": "dark", "timeout": 30}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("my_config", "generic_template", "", "mem", "cfg_v1.json")

		raw, err := generictemplate.Load(context.Background(), reg, "my_config")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		expected := `{"theme": "dark", "timeout": 30}`
		if string(raw) != expected {
			t.Errorf("expected %q, got %q", expected, raw)
		}
	})

	t.Run("Load invalid JSON returns error", func(t *testing.T) {
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"cfg_invalid.json": []byte(`{invalid-json}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("my_config", "generic_template", "", "mem", "cfg_invalid.json")

		_, err := generictemplate.Load(context.Background(), reg, "my_config")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("Load missing returns ErrNotFound", func(t *testing.T) {
		reg := artifact.NewRegistry()
		_, err := generictemplate.Load(context.Background(), reg, "missing")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}
