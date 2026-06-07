package uiprojector_test

import (
	"context"
	"testing"

	"github.com/OpenNSW/core/uiprojector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormProjector_Project(t *testing.T) {
	ctx := context.Background()
	p := uiprojector.NewFormProjector()

	t.Run("returns FormContent populated from schema, uiSchema, and data", func(t *testing.T) {
		template := []byte(`{"schema": {"type": "object"}, "uiSchema": {"ui:order": ["name"]}}`)
		data := map[string]any{"name": "John"}

		out, err := p.Project(ctx, template, data)
		require.NoError(t, err)
		assert.Equal(t, uiprojector.SectionTypeForm, out.Type)

		fc, ok := out.Content.(uiprojector.FormContent)
		require.True(t, ok, "expected FormContent, got %T", out.Content)
		assert.Equal(t, map[string]any{"type": "object"}, fc.Schema)
		assert.Equal(t, map[string]any{"ui:order": []any{"name"}}, fc.UISchema)
		assert.Equal(t, data, fc.Data)
	})

	t.Run("uiSchema is nil when absent from template", func(t *testing.T) {
		template := []byte(`{"schema": {"type": "object"}}`)
		out, err := p.Project(ctx, template, nil)
		require.NoError(t, err)

		fc := out.Content.(uiprojector.FormContent)
		assert.NotNil(t, fc.Schema)
		assert.Nil(t, fc.UISchema)
		assert.Nil(t, fc.Data)
	})

	t.Run("empty JSON object yields nil schema and uiSchema but preserves data", func(t *testing.T) {
		out, err := p.Project(ctx, []byte("{}"), "data")
		require.NoError(t, err)

		fc := out.Content.(uiprojector.FormContent)
		assert.Nil(t, fc.Schema)
		assert.Nil(t, fc.UISchema)
		assert.Equal(t, "data", fc.Data)
	})

	t.Run("returns error on invalid JSON", func(t *testing.T) {
		_, err := p.Project(ctx, []byte("not json"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "form_projector")
		assert.Contains(t, err.Error(), "failed to parse schema")
	})
}

func TestMarkdownProjector_Project(t *testing.T) {
	ctx := context.Background()
	p := uiprojector.NewMarkdownProjector()

	t.Run("substitutes data fields into template", func(t *testing.T) {
		out, err := p.Project(ctx, []byte("Hello {{.Name}}!"), map[string]any{"Name": "World"})
		require.NoError(t, err)
		assert.Equal(t, uiprojector.SectionTypeMarkdown, out.Type)
		assert.Equal(t, "Hello World!", out.Content)
	})

	t.Run("returns template verbatim when there are no placeholders", func(t *testing.T) {
		out, err := p.Project(ctx, []byte("static text"), nil)
		require.NoError(t, err)
		assert.Equal(t, "static text", out.Content)
	})

	t.Run("returns empty string for empty template", func(t *testing.T) {
		out, err := p.Project(ctx, []byte(""), nil)
		require.NoError(t, err)
		assert.Equal(t, "", out.Content)
	})

	t.Run("renders <no value> for missing fields under default text/template options", func(t *testing.T) {
		out, err := p.Project(ctx, []byte("Hello {{.Missing}}"), map[string]any{})
		require.NoError(t, err)
		assert.Contains(t, out.Content.(string), "<no value>")
	})

	t.Run("extracts template string from json wrapper and projects it", func(t *testing.T) {
		templateJSON := []byte(`{"id": "test-id", "template": "Hello {{.Name}}!"}`)
		out, err := p.Project(ctx, templateJSON, map[string]any{"Name": "JSON"})
		require.NoError(t, err)
		assert.Equal(t, "Hello JSON!", out.Content)
	})

	t.Run("returns parse error for malformed template syntax", func(t *testing.T) {
		_, err := p.Project(ctx, []byte("{{.Name"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "markdown_projector")
		assert.Contains(t, err.Error(), "failed to parse template")
	})
}

func TestRawProjector_Project(t *testing.T) {
	ctx := context.Background()
	p := uiprojector.NewRawProjector()

	t.Run("returns data unchanged for map input", func(t *testing.T) {
		data := map[string]any{"foo": "bar"}
		out, err := p.Project(ctx, []byte("ignored"), data)
		require.NoError(t, err)
		assert.Equal(t, uiprojector.SectionTypeRaw, out.Type)
		assert.Equal(t, data, out.Content)
	})

	t.Run("returns nil when data is nil", func(t *testing.T) {
		out, err := p.Project(ctx, nil, nil)
		require.NoError(t, err)
		assert.Nil(t, out.Content)
	})

	t.Run("ignores template content entirely", func(t *testing.T) {
		out, err := p.Project(ctx, []byte("garbage that would break other projectors"), 42)
		require.NoError(t, err)
		assert.Equal(t, 42, out.Content)
	})
}
