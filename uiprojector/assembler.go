package uiprojector

import (
	"context"
	"fmt"
)

// TemplateProvider abstracts the resolution of TemplateID to raw bytes.
type TemplateProvider interface {
	GetTemplate(ctx context.Context, templateID string) ([]byte, error)
}

// Assembler transforms a Blueprint and Facts into a list of rendered Sections.
type Assembler struct {
	templateProvider TemplateProvider
	projectors       map[ProjectorType]Projector
}

// NewAssembler builds an Assembler from a TemplateProvider and a slice of Projectors.
// Each projector's Type() is used as its registration key; duplicate types return an error.
func NewAssembler(tp TemplateProvider, projectors []Projector) (*Assembler, error) {
	if tp == nil {
		return nil, fmt.Errorf("uiprojector: template provider is required")
	}

	registry := make(map[ProjectorType]Projector, len(projectors))
	for _, p := range projectors {
		if p == nil {
			return nil, fmt.Errorf("uiprojector: nil projector in registration list")
		}
		t := p.Type()
		if t == "" {
			return nil, fmt.Errorf("uiprojector: projector %T returned empty Type()", p)
		}
		if _, exists := registry[t]; exists {
			return nil, fmt.Errorf("uiprojector: duplicate projector type %q", t)
		}
		registry[t] = p
	}

	return &Assembler{
		templateProvider: tp,
		projectors:       registry,
	}, nil
}

// Assemble is the "pure" transformation logic.
func (a *Assembler) Assemble(ctx context.Context, blueprint *Blueprint, facts Facts) (map[string]Section, error) {
	if blueprint == nil {
		return nil, fmt.Errorf("assembler: blueprint is nil")
	}

	// TODO: Should add a cache to cache the frequently fetched templates. Should decide whether the template should be from the TemplateProvider level or This Level.

	sections := make(map[string]Section, len(blueprint.Sections))

	for zone, sb := range blueprint.Sections {
		// 1. Visibility Check
		if !ShouldRender(sb, facts) {
			continue
		}

		// 2. Resolve Projector (Fail fast)
		proj, ok := a.projectors[ProjectorType(sb.Projector)]
		if !ok {
			return nil, fmt.Errorf("assembler: unknown projector %s", sb.Projector)
		}

		// 3. Fetch Template
		templateContent, err := a.templateProvider.GetTemplate(ctx, sb.TemplateID)
		if err != nil {
			return nil, fmt.Errorf("assembler: failed to fetch template %s: %w", sb.TemplateID, err)
		}

		// 4. Pluck Data from Registry via DataKey
		var sectionData any
		if sb.DataKey != "" {
			sectionData = facts.Data[sb.DataKey]
		} else {
			sectionData = facts.Data
		}

		// 5. Project
		projection, err := proj.Project(ctx, templateContent, sectionData)
		if err != nil {
			return nil, fmt.Errorf("assembler: projection failed for section %s: %w", zone, err)
		}
		if projection.Type == "" {
			return nil, fmt.Errorf("assembler: projector %s returned empty Projection.Type for section %s", sb.Projector, zone)
		}

		sections[zone] = Section{
			Type:    projection.Type,
			Title:   sb.Title,
			Content: projection.Content,
		}
	}

	return sections, nil
}
