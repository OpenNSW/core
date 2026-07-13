// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package generictemplate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/generictemplate"
	"github.com/OpenNSW/core/artifact/testutil"
)

func TestGenericTemplateAdapter(t *testing.T) {
	t.Run("Load returns unwrapped raw JSON template", func(t *testing.T) {
		m := testutil.MemLoader{
			"cfg_v1.json": []byte(`{"theme": "dark", "timeout": 30}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("my_config", "generic_template", "", "cfg_v1.json")

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
		m := testutil.MemLoader{
			"cfg_invalid.json": []byte(`{invalid-json}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("my_config", "generic_template", "", "cfg_invalid.json")

		_, err := generictemplate.Load(context.Background(), reg, "my_config")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("Load missing returns ErrNotFound", func(t *testing.T) {
		reg := artifact.NewRegistry(testutil.MemLoader{})
		_, err := generictemplate.Load(context.Background(), reg, "missing")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}
