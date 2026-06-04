package artifact_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"email_v1.json": []byte(`{"subject":"Hello V1"}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("welcome", "email", "v1", "mem", "email_v1.json")

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
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"email_single.json": []byte(`{"subject":"Hello Single"}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("welcome", "email", "", "mem", "email_single.json")

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
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"email_v1.json":  []byte(`{"subject":"Hello V1"}`),
			"email_v2.json":  []byte(`{"subject":"Hello V2"}`),
			"email_v10.json": []byte(`{"subject":"Hello V10"}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("welcome", "email", "v1", "mem", "email_v1.json")
		reg.RegisterArtifact("welcome", "email", "v2", "mem", "email_v2.json")
		reg.RegisterArtifact("welcome", "email", "v10", "mem", "email_v10.json")

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
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"email_v1.json": []byte(`{"subject":"Hello V1"}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("welcome", "email", "v1", "mem", "email_v1.json")

		_, err := artifact.Get[fakeEmail](context.Background(), reg, "welcome", "v2")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	// Scenario 5: No manifest row for (id, kind) at all
	t.Run("Get or Latest with no manifest row", func(t *testing.T) {
		reg := artifact.NewRegistry()
		_, errGet := artifact.Get[fakeEmail](context.Background(), reg, "welcome", "v1")
		if !errors.Is(errGet, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound for Get, got %v", errGet)
		}

		_, errLatest := artifact.Latest[fakeEmail](context.Background(), reg, "welcome")
		if !errors.Is(errLatest, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound for Latest, got %v", errLatest)
		}
	})

	// Scenario 6: Manifest references unregistered loader type
	t.Run("References unregistered loader type", func(t *testing.T) {
		reg := artifact.NewRegistry()
		reg.RegisterArtifact("welcome", "email", "v1", "mem", "email_v1.json")

		_, err := artifact.Get[fakeEmail](context.Background(), reg, "welcome", "v1")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected wiring error (not ErrNotFound), got %v", err)
		}
		expectedSubStr := `wants loader "mem"`
		if !strings.Contains(err.Error(), expectedSubStr) {
			t.Errorf("expected error containing %q, got %q", expectedSubStr, err.Error())
		}
	})

	// Scenario 7: Wrong-shape bytes (Parse validation fails)
	t.Run("Wrong shape bytes returns error instead of panic", func(t *testing.T) {
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"email_invalid.json": []byte(`{"not_subject":"Hello"}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("welcome", "email", "v1", "mem", "email_invalid.json")

		_, err := artifact.Get[fakeEmail](context.Background(), reg, "welcome", "v1")
		if err == nil {
			t.Fatal("expected parse error, got nil")
		}
		if errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected parse/validation error, not ErrNotFound, got %v", err)
		}
	})

	// Scenario 8: Loader returns a non-ErrNotFound error
	t.Run("Loader returns non-ErrNotFound error", func(t *testing.T) {
		reg := artifact.NewRegistry()
		errInternal := errors.New("disk crash")
		var m errorLoader = func(ctx context.Context, path string) ([]byte, error) {
			return nil, errInternal
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("welcome", "email", "v1", "mem", "email_v1.json")

		_, err := artifact.Get[fakeEmail](context.Background(), reg, "welcome", "v1")
		if !errors.Is(err, errInternal) {
			t.Errorf("expected internal loader error to surface, got %v", err)
		}
	})

	// Scenario 9: A second, locally-defined artifact type with a custom Kind fetches fine
	t.Run("Custom Kind fetches fine (extensibility proof)", func(t *testing.T) {
		reg := artifact.NewRegistry()
		m := testutil.MemLoader{
			"rules.json": []byte(`{"rules":["rule1", "rule2"]}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("import_rules", "custom_ruleset", "v1", "mem", "rules.json")

		rules, err := artifact.Get[customArtifact](context.Background(), reg, "import_rules", "v1")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(rules.Rules) != 2 || rules.Rules[0] != "rule1" {
			t.Errorf("expected rules ['rule1', 'rule2'], got %v", rules.Rules)
		}
	})

	// Scenario 10: Custom WithVersionComparator changes which version Latest picks
	t.Run("Custom version comparator is honored", func(t *testing.T) {
		customLess := func(a, b string) bool {
			return a > b
		}
		reg := artifact.NewRegistry(artifact.WithVersionComparator(customLess))
		m := testutil.MemLoader{
			"email_v1.json": []byte(`{"subject":"Hello V1"}`),
			"email_v2.json": []byte(`{"subject":"Hello V2"}`),
		}
		reg.RegisterLoader("mem", m)
		reg.RegisterArtifact("welcome", "email", "v1", "mem", "email_v1.json")
		reg.RegisterArtifact("welcome", "email", "v2", "mem", "email_v2.json")

		email, err := artifact.Latest[fakeEmail](context.Background(), reg, "welcome")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if email.Subject != "Hello V1" {
			t.Errorf("expected subject 'Hello V1' due to custom comparator, got %q", email.Subject)
		}
	})
}

func TestManifest(t *testing.T) {
	t.Run("RegisterFromConfig successfully registers", func(t *testing.T) {
		reg := artifact.NewRegistry()
		reg.RegisterLoader("mem", testutil.MemLoader{})

		cfg := artifact.ManifestConfig{
			Artifacts: []artifact.ManifestRow{
				{
					ID:      "import_clearance",
					Kind:    "workflow",
					Version: "v3",
					Loader:  "mem",
					Path:    "wf/import_clearance.v3.json",
				},
			},
		}

		err := artifact.RegisterFromConfig(reg, cfg)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("RegisterFromConfig fails with unregistered loader type", func(t *testing.T) {
		reg := artifact.NewRegistry()
		cfg := artifact.ManifestConfig{
			Artifacts: []artifact.ManifestRow{
				{
					ID:      "import_clearance",
					Kind:    "workflow",
					Version: "v3",
					Loader:  "missing",
					Path:    "wf/import_clearance.v3.json",
				},
			},
		}

		err := artifact.RegisterFromConfig(reg, cfg)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), `references unregistered loader type "missing"`) {
			t.Errorf("expected error message to contain unregistered loader type warning, got %v", err)
		}
	})
}

func TestLoadManifestFile(t *testing.T) {
	t.Run("Load JSON manifest", func(t *testing.T) {
		tempDir := t.TempDir()
		jsonPath := filepath.Join(tempDir, "manifest.json")
		content := `{
			"artifacts": [
				{ "id": "test_id", "kind": "email", "version": "v1", "loader": "local", "path": "path/to/email.json" }
			]
		}`
		if err := os.WriteFile(jsonPath, []byte(content), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		cfg, err := artifact.LoadManifestFile(jsonPath)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(cfg.Artifacts) != 1 || cfg.Artifacts[0].ID != "test_id" {
			t.Errorf("unexpected manifest content: %+v", cfg)
		}
	})

	t.Run("Load YAML manifest", func(t *testing.T) {
		tempDir := t.TempDir()
		yamlPath := filepath.Join(tempDir, "manifest.yaml")
		content := `
artifacts:
  - id: test_id_yaml
    kind: email
    version: v1
    loader: local
    path: path/to/email.json
`
		if err := os.WriteFile(yamlPath, []byte(content), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		cfg, err := artifact.LoadManifestFile(yamlPath)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(cfg.Artifacts) != 1 || cfg.Artifacts[0].ID != "test_id_yaml" {
			t.Errorf("unexpected manifest content: %+v", cfg)
		}
	})
}

func ExampleLatest() {
	// Create registry
	reg := artifact.NewRegistry()

	// Create and register loader
	m := testutil.MemLoader{
		"welcome.json": []byte(`{"subject":"Welcome to OpenNSW!"}`),
	}
	reg.RegisterLoader("mem", m)

	// Register artifact row
	reg.RegisterArtifact("welcome_email", "email", "", "mem", "welcome.json")

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
