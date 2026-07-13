// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
)

type fakeEmail struct {
	Subject string `json:"subject"`
}

func (fakeEmail) Kind() artifact.Kind { return "email" }

func (t *fakeEmail) Parse(raw []byte) error {
	var temp struct {
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal(raw, &temp); err != nil {
		return fmt.Errorf("json unmarshal: %w", err)
	}
	if temp.Subject == "" {
		return fmt.Errorf("missing subject")
	}
	t.Subject = temp.Subject
	return nil
}

type customArtifact struct {
	Rules []string `json:"rules"`
}

func (customArtifact) Kind() artifact.Kind { return "custom_ruleset" }

func (c *customArtifact) Parse(raw []byte) error {
	var temp struct {
		Rules []string `json:"rules"`
	}
	if err := json.Unmarshal(raw, &temp); err != nil {
		return fmt.Errorf("json unmarshal: %w", err)
	}
	if len(temp.Rules) == 0 {
		return fmt.Errorf("custom ruleset: no rules")
	}
	c.Rules = temp.Rules
	return nil
}

type errorLoader func(ctx context.Context, path string) ([]byte, error)

func (e errorLoader) Load(ctx context.Context, path string) ([]byte, error) {
	return e(ctx, path)
}

func TestRegistry(t *testing.T) {
	// Scenario 1: Get[fakeEmail] exact version present
	t.Run("Get exact version present", func(t *testing.T) {
		m := testutil.MemLoader{
			"email_v1.json": []byte(`{"subject":"Hello V1"}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("welcome", "email", "v1", "email_v1.json")

		email, err := artifact.Get[fakeEmail](context.Background(), reg, "welcome", "v1")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if email.Subject != "Hello V1" {
			t.Errorf("expected subject 'Hello V1', got %q", email.Subject)
		}
	})

	// Scenario 2: Latest with one "" version
	t.Run("Latest with single unversioned", func(t *testing.T) {
		m := testutil.MemLoader{
			"email_single.json": []byte(`{"subject":"Hello Single"}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("welcome", "email", "", "email_single.json")

		email, err := artifact.Latest[fakeEmail](context.Background(), reg, "welcome")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if email.Subject != "Hello Single" {
			t.Errorf("expected subject 'Hello Single', got %q", email.Subject)
		}
	})

	// Scenario 3: Latest with versions v1, v2, v10 present
	t.Run("Latest with v1, v2, v10 numeric sorting", func(t *testing.T) {
		m := testutil.MemLoader{
			"email_v1.json":  []byte(`{"subject":"Hello V1"}`),
			"email_v2.json":  []byte(`{"subject":"Hello V2"}`),
			"email_v10.json": []byte(`{"subject":"Hello V10"}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("welcome", "email", "v1", "email_v1.json")
		reg.RegisterArtifact("welcome", "email", "v2", "email_v2.json")
		reg.RegisterArtifact("welcome", "email", "v10", "email_v10.json")

		email, err := artifact.Latest[fakeEmail](context.Background(), reg, "welcome")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if email.Subject != "Hello V10" {
			t.Errorf("expected subject 'Hello V10', got %q", email.Subject)
		}
	})

	// Scenario 4: Get for a version that doesn't exist
	t.Run("Get non-existent version", func(t *testing.T) {
		m := testutil.MemLoader{
			"email_v1.json": []byte(`{"subject":"Hello V1"}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("welcome", "email", "v1", "email_v1.json")

		_, err := artifact.Get[fakeEmail](context.Background(), reg, "welcome", "v2")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	// Scenario 5: No manifest row for (id, kind) at all
	t.Run("Get or Latest with no manifest row", func(t *testing.T) {
		reg := artifact.NewRegistry(testutil.MemLoader{})
		_, errGet := artifact.Get[fakeEmail](context.Background(), reg, "welcome", "v1")
		if !errors.Is(errGet, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound for Get, got %v", errGet)
		}

		_, errLatest := artifact.Latest[fakeEmail](context.Background(), reg, "welcome")
		if !errors.Is(errLatest, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound for Latest, got %v", errLatest)
		}
	})

	// Scenario 6: Wrong-shape bytes (Parse validation fails)
	t.Run("Wrong shape bytes returns error instead of panic", func(t *testing.T) {
		m := testutil.MemLoader{
			"email_invalid.json": []byte(`{"not_subject":"Hello"}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("welcome", "email", "v1", "email_invalid.json")

		_, err := artifact.Get[fakeEmail](context.Background(), reg, "welcome", "v1")
		if err == nil {
			t.Fatal("expected parse error, got nil")
		}
		if errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected parse/validation error, not ErrNotFound, got %v", err)
		}
	})

	// Scenario 7: Loader returns a non-ErrNotFound error
	t.Run("Loader returns non-ErrNotFound error", func(t *testing.T) {
		errInternal := errors.New("disk crash")
		var m errorLoader = func(ctx context.Context, path string) ([]byte, error) {
			return nil, errInternal
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("welcome", "email", "v1", "email_v1.json")

		_, err := artifact.Get[fakeEmail](context.Background(), reg, "welcome", "v1")
		if !errors.Is(err, errInternal) {
			t.Errorf("expected internal loader error to surface, got %v", err)
		}
	})

	// Scenario 8: A second, locally-defined artifact type with a custom Kind fetches fine
	t.Run("Custom Kind fetches fine (extensibility proof)", func(t *testing.T) {
		m := testutil.MemLoader{
			"rules.json": []byte(`{"rules":["rule1", "rule2"]}`),
		}
		reg := artifact.NewRegistry(m)
		reg.RegisterArtifact("import_rules", "custom_ruleset", "v1", "rules.json")

		rules, err := artifact.Get[customArtifact](context.Background(), reg, "import_rules", "v1")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(rules.Rules) != 2 || rules.Rules[0] != "rule1" {
			t.Errorf("expected rules ['rule1', 'rule2'], got %v", rules.Rules)
		}
	})

	// Scenario 9: Custom WithVersionComparator changes which version Latest picks
	t.Run("Custom version comparator is honored", func(t *testing.T) {
		customLess := func(a, b string) bool {
			return a > b
		}
		m := testutil.MemLoader{
			"email_v1.json": []byte(`{"subject":"Hello V1"}`),
			"email_v2.json": []byte(`{"subject":"Hello V2"}`),
		}
		reg := artifact.NewRegistry(m, artifact.WithVersionComparator(customLess))
		reg.RegisterArtifact("welcome", "email", "v1", "email_v1.json")
		reg.RegisterArtifact("welcome", "email", "v2", "email_v2.json")

		email, err := artifact.Latest[fakeEmail](context.Background(), reg, "welcome")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if email.Subject != "Hello V1" {
			t.Errorf("expected subject 'Hello V1' due to custom comparator, got %q", email.Subject)
		}
	})

	// Scenario 10: A nil loader is a wiring bug and must panic at construction.
	t.Run("NewRegistry panics on nil loader", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("expected panic on nil loader, got none")
			}
		}()
		_ = artifact.NewRegistry(nil)
	})
}

func TestManifest(t *testing.T) {
	t.Run("RegisterFromConfig successfully registers", func(t *testing.T) {
		reg := artifact.NewRegistry(testutil.MemLoader{})

		cfg := artifact.ManifestConfig{
			Artifacts: []artifact.ManifestRow{
				{
					ID:      "import_clearance",
					Kind:    "workflow",
					Version: "v3",
					Path:    "wf/import_clearance.v3.json",
				},
			},
		}

		if err := artifact.RegisterFromConfig(reg, cfg); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("RegisterFromConfig fails on a row missing its path", func(t *testing.T) {
		reg := artifact.NewRegistry(testutil.MemLoader{})
		cfg := artifact.ManifestConfig{
			Artifacts: []artifact.ManifestRow{
				{
					ID:      "import_clearance",
					Kind:    "workflow",
					Version: "v3",
					Path:    "",
				},
			},
		}

		err := artifact.RegisterFromConfig(reg, cfg)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "id, kind, and path are all required") {
			t.Errorf("expected required-fields error, got %v", err)
		}
	})
}

func TestLoadManifest(t *testing.T) {
	t.Run("Load manifest from the source root", func(t *testing.T) {
		m := testutil.MemLoader{
			artifact.ManifestFilename: []byte(`{
				"artifacts": [
					{ "id": "test_id", "kind": "email", "version": "v1", "path": "path/to/email.json" }
				]
			}`),
		}

		cfg, err := artifact.LoadManifest(context.Background(), m)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(cfg.Artifacts) != 1 || cfg.Artifacts[0].ID != "test_id" {
			t.Errorf("unexpected manifest content: %+v", cfg)
		}
	})

	t.Run("Missing manifest surfaces the loader error", func(t *testing.T) {
		cfg, err := artifact.LoadManifest(context.Background(), testutil.MemLoader{})
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
		if len(cfg.Artifacts) != 0 {
			t.Errorf("expected empty manifest on error, got %+v", cfg)
		}
	})
}

func ExampleLatest() {
	// Create and register the single loader, then the registry on top of it.
	m := testutil.MemLoader{
		"welcome.json": []byte(`{"subject":"Welcome to OpenNSW!"}`),
	}
	reg := artifact.NewRegistry(m)

	// Register artifact row
	reg.RegisterArtifact("welcome_email", "email", "", "welcome.json")

	// Fetch latest welcome email
	ctx := context.Background()
	email, err := artifact.Latest[fakeEmail](ctx, reg, "welcome_email")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println(email.Subject)
	// Output: Welcome to OpenNSW!
}
