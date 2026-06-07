package uiprojector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"text/template"
)

// ProjectorType identifies a projector implementation. External packages may
// declare their own ProjectorType constants to register custom projectors.
type ProjectorType string

// Projection is the result of a projector's Project call. Type identifies the
// render shape the frontend should use; Content is the payload it renders.
// A single projector may emit different Types per invocation when the choice
// depends on runtime data (e.g. a payment projector switching between REDIRECT
// and DESCRIPTION based on the configured payment method).
type Projection struct {
	Type    SectionType
	Content any
}

// Projector defines the interface for transforming raw template + data into a UI payload.
// Type returns the identifier under which the projector is registered; it must match
// the SectionBlueprint.Projector values used in blueprints. The render type used by
// the frontend is carried on the Projection returned from Project, not on Type().
type Projector interface {
	Type() ProjectorType
	Project(ctx context.Context, templateContent []byte, data any) (Projection, error)
}

// FormProjector projects raw JSON schema into a FormContent payload.
type FormProjector struct{}

func NewFormProjector() *FormProjector {
	return &FormProjector{}
}

func (p *FormProjector) Type() ProjectorType { return ProjectorForm }

func (p *FormProjector) Project(ctx context.Context, templateContent []byte, data any) (Projection, error) {
	var schema map[string]any
	if err := json.Unmarshal(templateContent, &schema); err != nil {
		return Projection{}, fmt.Errorf("form_projector: failed to parse schema: %w", err)
	}

	return Projection{
		Type: SectionTypeForm,
		Content: FormContent{
			Schema:   schema["schema"],
			UISchema: schema["uiSchema"],
			Data:     data,
		},
	}, nil
}

// MarkdownProjector projects a markdown template using Go's text/template.
type MarkdownProjector struct{}

func NewMarkdownProjector() *MarkdownProjector {
	return &MarkdownProjector{}
}

func (p *MarkdownProjector) Type() ProjectorType { return ProjectorMarkdown }

func (p *MarkdownProjector) Project(ctx context.Context, templateContent []byte, data any) (Projection, error) {
	var wrapper struct {
		Template string `json:"template"`
	}
	templateStr := string(templateContent)
	if json.Unmarshal(templateContent, &wrapper) == nil && wrapper.Template != "" {
		templateStr = wrapper.Template
	}

	tmpl, err := template.New("markdown").Parse(templateStr)
	if err != nil {
		return Projection{}, fmt.Errorf("markdown_projector: failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return Projection{}, fmt.Errorf("markdown_projector: failed to execute template: %w", err)
	}

	return Projection{Type: SectionTypeMarkdown, Content: buf.String()}, nil
}

// RawProjector returns the data as-is without any transformation.
type RawProjector struct{}

func NewRawProjector() *RawProjector {
	return &RawProjector{}
}

func (p *RawProjector) Type() ProjectorType { return ProjectorRaw }

func (p *RawProjector) Project(ctx context.Context, templateContent []byte, data any) (Projection, error) {
	return Projection{Type: SectionTypeRaw, Content: data}, nil
}
