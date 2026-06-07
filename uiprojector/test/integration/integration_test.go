package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenNSW/core/uiprojector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fileTemplateProvider implements uiprojector.TemplateProvider by reading from local files.
type fileTemplateProvider struct {
	basePath string
}

func (p *fileTemplateProvider) GetTemplate(ctx context.Context, templateID string) ([]byte, error) {
	path := filepath.Join(p.basePath, templateID)
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read template %s: %w", templateID, err)
	}
	return content, nil
}

func TestUIProjectorIntegration(t *testing.T) {
	ctx := context.Background()
	testDataPath := "testdata/templates"

	// 1. Initialize Assembler with real projectors
	provider := &fileTemplateProvider{basePath: testDataPath}
	projectors := []uiprojector.Projector{
		uiprojector.NewFormProjector(),
		uiprojector.NewMarkdownProjector(),
	}
	assembler, err := uiprojector.NewAssembler(provider, projectors)
	require.NoError(t, err)

	// 2. Define a Blueprint for a complex view
	blueprint := &uiprojector.Blueprint{
		ID: "consignment_review",
		Sections: map[string]uiprojector.SectionBlueprint{
			"header": {
				Title:      "Consignment Status",
				TemplateID: "markdown.md",
				Projector:  "MARKDOWN",
				DataKey:    "summary",
			},
			"declaration_form": {
				Title:      "Import Declaration",
				TemplateID: "form.json",
				Projector:  "FORM",
				DataKey:    "declaration",
				VisibleWhen: &uiprojector.VisibleWhen{
					States: []string{"INITIALIZED", "IN_PROGRESS"},
				},
			},
			"approval_note": {
				Title:      "Final Approval",
				TemplateID: "markdown.md",
				Projector:  "MARKDOWN",
				DataKey:    "approval",
				VisibleWhen: &uiprojector.VisibleWhen{
					States: []string{"APPROVED"},
				},
			},
		},
	}

	t.Run("Assemble InProgress State", func(t *testing.T) {
		facts := uiprojector.Facts{
			State: "IN_PROGRESS",
			Data: map[string]any{
				"summary": map[string]any{
					"name":   "Trader Joe",
					"status": "In Progress",
				},
				"declaration": map[string]any{
					"name":  "John Doe",
					"email": "john@example.com",
				},
			},
		}

		sections, err := assembler.Assemble(ctx, blueprint, facts)
		require.NoError(t, err)

		// Should have header and declaration form (not approval note)
		assert.Len(t, sections, 2)
		assert.Contains(t, sections, "header")
		assert.Contains(t, sections, "declaration_form")
		assert.NotContains(t, sections, "approval_note")

		// Verify Markdown content
		assert.Contains(t, sections["header"].Content.(string), "Welcome, Trader Joe!")
		assert.Contains(t, sections["header"].Content.(string), "In Progress")

		// Verify Form content
		formContent := sections["declaration_form"].Content.(uiprojector.FormContent)
		assert.NotNil(t, formContent.Schema)
		assert.Equal(t, "John Doe", formContent.Data.(map[string]any)["name"])
	})

	t.Run("Assemble Approved State (Visibility Logic)", func(t *testing.T) {
		facts := uiprojector.Facts{
			State: "APPROVED",
			Data: map[string]any{
				"summary": map[string]any{
					"name":   "Trader Joe",
					"status": "Completed",
				},
				"approval": map[string]any{
					"name":   "Officer Smith",
					"status": "APPROVED",
				},
			},
		}

		sections, err := assembler.Assemble(ctx, blueprint, facts)
		require.NoError(t, err)

		// Should have header and approval note (not declaration form)
		assert.Len(t, sections, 2)
		assert.Contains(t, sections, "header")
		assert.Contains(t, sections, "approval_note")
		assert.NotContains(t, sections, "declaration_form")

		assert.Contains(t, sections["approval_note"].Content.(string), "Welcome, Officer Smith!")
	})

	t.Run("DataKey Requirement Validation", func(t *testing.T) {
		// Add a section that requires a specific data key to be present
		blueprint.Sections["conditional_section"] = uiprojector.SectionBlueprint{
			TemplateID: "markdown.md",
			Projector:  "MARKDOWN",
			DataKey:    "extra_info",
			VisibleWhen: &uiprojector.VisibleWhen{
				RequireDataKey: "extra_info",
			},
		}

		facts := uiprojector.Facts{
			State: "IN_PROGRESS",
			Data: map[string]any{
				"summary": map[string]any{"name": "Joe", "status": "Test"},
			},
		}

		sections, err := assembler.Assemble(ctx, blueprint, facts)
		require.NoError(t, err)

		// conditional_section should be missing because extra_info data key is missing
		assert.NotContains(t, sections, "conditional_section")

		// Now add the data key
		facts.Data["extra_info"] = map[string]any{"name": "Admin", "status": "Online"}
		sections, err = assembler.Assemble(ctx, blueprint, facts)
		require.NoError(t, err)

		assert.Contains(t, sections, "conditional_section", "conditional_section should be present when DataKey exists")
	})
}
