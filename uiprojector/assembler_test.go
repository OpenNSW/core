package uiprojector_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenNSW/core/uiprojector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubTemplateProvider struct {
	templates map[string][]byte
	err       error
}

func (s *stubTemplateProvider) GetTemplate(_ context.Context, id string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	t, ok := s.templates[id]
	if !ok {
		return nil, errors.New("template not found: " + id)
	}
	return t, nil
}

type stubProjector struct {
	typeName     uiprojector.ProjectorType
	renderType   uiprojector.SectionType // when empty, defaults to SectionType(typeName)
	lastTemplate []byte
	lastData     any
	out          any
	err          error
}

func (p *stubProjector) Type() uiprojector.ProjectorType { return p.typeName }

func (p *stubProjector) Project(_ context.Context, template []byte, data any) (uiprojector.Projection, error) {
	p.lastTemplate = template
	p.lastData = data
	if p.err != nil {
		return uiprojector.Projection{}, p.err
	}
	rt := p.renderType
	if rt == "" {
		rt = uiprojector.SectionType(p.typeName)
	}
	return uiprojector.Projection{Type: rt, Content: p.out}, nil
}

func TestNewAssembler(t *testing.T) {
	t.Run("returns error on nil TemplateProvider", func(t *testing.T) {
		asm, err := uiprojector.NewAssembler(nil, nil)
		assert.Error(t, err)
		assert.Nil(t, asm)
		assert.Contains(t, err.Error(), "template provider is required")
	})

	t.Run("returns error on duplicate projector type", func(t *testing.T) {
		asm, err := uiprojector.NewAssembler(&stubTemplateProvider{}, []uiprojector.Projector{
			&stubProjector{typeName: "DUP"},
			&stubProjector{typeName: "DUP"},
		})
		assert.Error(t, err)
		assert.Nil(t, asm)
		assert.Contains(t, err.Error(), "duplicate projector type")
	})

	t.Run("returns error on empty Type()", func(t *testing.T) {
		asm, err := uiprojector.NewAssembler(&stubTemplateProvider{}, []uiprojector.Projector{
			&stubProjector{typeName: ""},
		})
		assert.Error(t, err)
		assert.Nil(t, asm)
		assert.Contains(t, err.Error(), "empty Type()")
	})

	t.Run("returns error on nil projector entry", func(t *testing.T) {
		asm, err := uiprojector.NewAssembler(&stubTemplateProvider{}, []uiprojector.Projector{nil})
		assert.Error(t, err)
		assert.Nil(t, asm)
		assert.Contains(t, err.Error(), "nil projector")
	})

	t.Run("does not retain caller's slice", func(t *testing.T) {
		list := []uiprojector.Projector{&stubProjector{typeName: "P1"}}
		tp := &stubTemplateProvider{templates: map[string][]byte{"t": []byte("x")}}
		asm, err := uiprojector.NewAssembler(tp, list)
		require.NoError(t, err)

		// Mutate the caller's slice; assembler keeps its own registry.
		list[0] = &stubProjector{typeName: "P2"}

		ctx := context.Background()
		bp := &uiprojector.Blueprint{Sections: map[string]uiprojector.SectionBlueprint{
			"zone1": {TemplateID: "t", Projector: "P1"},
		}}
		_, err = asm.Assemble(ctx, bp, uiprojector.Facts{})
		assert.NoError(t, err, "Assembler should still have P1")

		bp2 := &uiprojector.Blueprint{Sections: map[string]uiprojector.SectionBlueprint{
			"zone1": {TemplateID: "t", Projector: "P2"},
		}}
		_, err = asm.Assemble(ctx, bp2, uiprojector.Facts{})
		assert.Error(t, err, "Assembler should NOT have P2")
	})
}

func TestAssembler_Assemble_HappyPath(t *testing.T) {
	ctx := context.Background()
	tp := &stubTemplateProvider{templates: map[string][]byte{
		"tpl-a": []byte("A"),
		"tpl-b": []byte("B"),
	}}
	pA := &stubProjector{typeName: "PA", out: "rendered-A"}
	pB := &stubProjector{typeName: "PB", out: "rendered-B"}
	asm, err := uiprojector.NewAssembler(tp, []uiprojector.Projector{pA, pB})
	require.NoError(t, err)

	blueprint := &uiprojector.Blueprint{
		ID: "bp",
		Sections: map[string]uiprojector.SectionBlueprint{
			"main":    {Title: "First", TemplateID: "tpl-a", Projector: "PA", DataKey: "alpha"},
			"sidebar": {Title: "Second", TemplateID: "tpl-b", Projector: "PB"},
		},
	}
	facts := uiprojector.Facts{
		State: "IN_PROGRESS",
		Data: map[string]any{
			"alpha": map[string]any{"x": 1},
			"beta":  "value",
		},
	}

	sections, err := asm.Assemble(ctx, blueprint, facts)
	require.NoError(t, err)
	require.Len(t, sections, 2)

	assert.Equal(t, "First", sections["main"].Title)
	assert.Equal(t, uiprojector.SectionType("PA"), sections["main"].Type)
	assert.Equal(t, "rendered-A", sections["main"].Content)
	assert.Equal(t, map[string]any{"x": 1}, pA.lastData, "DataKey should pluck alpha")
	assert.Equal(t, []byte("A"), pA.lastTemplate)

	assert.Equal(t, uiprojector.SectionType("PB"), sections["sidebar"].Type)
	assert.Equal(t, facts.Data, pB.lastData, "empty DataKey should pass full Data")
}

func TestAssembler_Assemble_SkipsHiddenSections(t *testing.T) {
	ctx := context.Background()
	tp := &stubTemplateProvider{templates: map[string][]byte{"t": []byte("x")}}
	p := &stubProjector{typeName: "P", out: "ok"}
	asm, err := uiprojector.NewAssembler(tp, []uiprojector.Projector{p})
	require.NoError(t, err)

	blueprint := &uiprojector.Blueprint{
		Sections: map[string]uiprojector.SectionBlueprint{
			"visible": {TemplateID: "t", Projector: "P"},
			"hidden": {TemplateID: "t", Projector: "P", VisibleWhen: &uiprojector.VisibleWhen{
				States: []string{"NEVER"},
			}},
		},
	}

	sections, err := asm.Assemble(ctx, blueprint, uiprojector.Facts{State: "ANY"})
	require.NoError(t, err)
	require.Len(t, sections, 1)
	assert.Contains(t, sections, "visible")
	assert.NotContains(t, sections, "hidden")
}

func TestAssembler_Assemble_EmptyBlueprint(t *testing.T) {
	ctx := context.Background()
	asm, err := uiprojector.NewAssembler(&stubTemplateProvider{}, nil)
	require.NoError(t, err)

	sections, err := asm.Assemble(ctx, &uiprojector.Blueprint{}, uiprojector.Facts{})
	require.NoError(t, err)
	assert.Empty(t, sections)
}

func TestAssembler_Assemble_BlueprintIsNil(t *testing.T) {
	ctx := context.Background()
	asm, err := uiprojector.NewAssembler(&stubTemplateProvider{}, nil)
	require.NoError(t, err)

	_, err = asm.Assemble(ctx, nil, uiprojector.Facts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blueprint is nil")
}

func TestAssembler_Assemble_TemplateFetchError(t *testing.T) {
	ctx := context.Background()
	fetchErr := errors.New("boom")
	tp := &stubTemplateProvider{err: fetchErr}
	asm, err := uiprojector.NewAssembler(tp, []uiprojector.Projector{&stubProjector{typeName: "P"}})
	require.NoError(t, err)

	bp := &uiprojector.Blueprint{Sections: map[string]uiprojector.SectionBlueprint{
		"main": {TemplateID: "missing", Projector: "P"},
	}}

	_, err = asm.Assemble(ctx, bp, uiprojector.Facts{})
	require.Error(t, err)
	assert.ErrorIs(t, err, fetchErr)
	assert.Contains(t, err.Error(), "missing", "error should mention the template ID")
}

func TestAssembler_Assemble_UnknownProjector(t *testing.T) {
	ctx := context.Background()
	tp := &stubTemplateProvider{templates: map[string][]byte{"t": []byte("x")}}
	asm, err := uiprojector.NewAssembler(tp, nil)
	require.NoError(t, err)

	bp := &uiprojector.Blueprint{Sections: map[string]uiprojector.SectionBlueprint{
		"main": {TemplateID: "t", Projector: "GHOST"},
	}}

	_, err = asm.Assemble(ctx, bp, uiprojector.Facts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown projector")
	assert.Contains(t, err.Error(), "GHOST")
}

func TestAssembler_Assemble_ProjectorError(t *testing.T) {
	ctx := context.Background()
	tp := &stubTemplateProvider{templates: map[string][]byte{"t": []byte("x")}}
	projErr := errors.New("render failed")
	p := &stubProjector{typeName: "P", err: projErr}
	asm, err := uiprojector.NewAssembler(tp, []uiprojector.Projector{p})
	require.NoError(t, err)

	bp := &uiprojector.Blueprint{Sections: map[string]uiprojector.SectionBlueprint{
		"main": {TemplateID: "t", Projector: "P"},
	}}

	_, err = asm.Assemble(ctx, bp, uiprojector.Facts{})
	require.Error(t, err)
	assert.ErrorIs(t, err, projErr)
	assert.Contains(t, err.Error(), "main", "error should mention the failing section's slot key")
}

type emptyTypeProjector struct{ typeName uiprojector.ProjectorType }

func (p *emptyTypeProjector) Type() uiprojector.ProjectorType { return p.typeName }
func (p *emptyTypeProjector) Project(_ context.Context, _ []byte, _ any) (uiprojector.Projection, error) {
	return uiprojector.Projection{Type: "", Content: "ok"}, nil
}

func TestAssembler_Assemble_EmptyProjectionType(t *testing.T) {
	ctx := context.Background()
	tp := &stubTemplateProvider{templates: map[string][]byte{"t": []byte("x")}}
	asm, err := uiprojector.NewAssembler(tp, []uiprojector.Projector{&emptyTypeProjector{typeName: "P"}})
	require.NoError(t, err)

	bp := &uiprojector.Blueprint{Sections: map[string]uiprojector.SectionBlueprint{
		"main": {TemplateID: "t", Projector: "P"},
	}}

	_, err = asm.Assemble(ctx, bp, uiprojector.Facts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty Projection.Type")
	assert.Contains(t, err.Error(), "main")
}

func TestAssembler_Assemble_DataKeyMissingPassesNil(t *testing.T) {
	ctx := context.Background()
	tp := &stubTemplateProvider{templates: map[string][]byte{"t": []byte("x")}}
	p := &stubProjector{typeName: "P", out: "ok"}
	asm, err := uiprojector.NewAssembler(tp, []uiprojector.Projector{p})
	require.NoError(t, err)

	bp := &uiprojector.Blueprint{Sections: map[string]uiprojector.SectionBlueprint{
		"main": {TemplateID: "t", Projector: "P", DataKey: "absent"},
	}}
	facts := uiprojector.Facts{Data: map[string]any{"other": 1}}

	_, err = asm.Assemble(ctx, bp, facts)
	require.NoError(t, err)
	assert.Nil(t, p.lastData, "DataKey lookup on missing key should pass nil")
}
