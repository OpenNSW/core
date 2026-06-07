package uiprojector_test

import (
	"testing"

	"github.com/OpenNSW/core/uiprojector"
	"github.com/stretchr/testify/assert"
)

func TestProjectorConstants(t *testing.T) {
	assert.Equal(t, uiprojector.ProjectorType("FORM"), uiprojector.ProjectorForm)
	assert.Equal(t, uiprojector.ProjectorType("MARKDOWN"), uiprojector.ProjectorMarkdown)
	assert.Equal(t, uiprojector.ProjectorType("RAW"), uiprojector.ProjectorRaw)
}

func TestDefaultProjectors_RegistersBuiltIns(t *testing.T) {
	p := uiprojector.DefaultProjectors()

	assert.Len(t, p, 3)
	byType := make(map[uiprojector.ProjectorType]uiprojector.Projector, len(p))
	for _, proj := range p {
		byType[proj.Type()] = proj
	}
	assert.IsType(t, &uiprojector.FormProjector{}, byType[uiprojector.ProjectorForm])
	assert.IsType(t, &uiprojector.MarkdownProjector{}, byType[uiprojector.ProjectorMarkdown])
	assert.IsType(t, &uiprojector.RawProjector{}, byType[uiprojector.ProjectorRaw])
}

func TestDefaultProjectors_ReturnsIndependentSlices(t *testing.T) {
	a := uiprojector.DefaultProjectors()
	b := uiprojector.DefaultProjectors()

	a[0] = uiprojector.NewRawProjector()

	assert.IsType(t, &uiprojector.FormProjector{}, b[0], "mutating one slice must not affect another")
}
