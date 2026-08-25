// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package uiprojector

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
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

	registeredTypes := make([]string, 0, len(registry))
	for t := range registry {
		registeredTypes = append(registeredTypes, string(t))
	}
	slog.Info("uiprojector assembler initialized", "types", registeredTypes)
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

	// Fail fast on a caller-contract violation: every claim the blueprint
	// references must be populated in Facts.Claims. This turns a silent,
	// hard-to-debug fail-closed (a typo or casing mismatch that hides a
	// section forever) into a loud error at call time.
	if err := validateClaims(blueprint, facts); err != nil {
		return nil, err
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

// validateClaims ensures every claim referenced by a section's RequireClaim
// rule is present in facts.Claims. Claim keys are matched exactly
// (case-sensitive), so this check catches the common failure mode where the
// blueprint and the caller disagree on a claim's spelling or casing: without
// it, the mismatch would silently hide the section with no error to trace.
// Presence is the contract — a claim explicitly set to false is a valid,
// intentional deny; a claim that is simply missing is treated as a bug.
func validateClaims(blueprint *Blueprint, facts Facts) error {
	seen := make(map[string]struct{})
	var missing []string
	for _, sb := range blueprint.Sections {
		if sb.VisibleWhen == nil {
			continue
		}
		claim := sb.VisibleWhen.RequireClaim
		if claim == "" {
			continue
		}
		if _, ok := facts.Claims[claim]; ok {
			continue
		}
		if _, dup := seen[claim]; dup {
			continue
		}
		seen[claim] = struct{}{}
		missing = append(missing, claim)
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing) // deterministic message; map iteration order is random
	return fmt.Errorf("assembler: blueprint references claim(s) %v not present in Facts.Claims; "+
		"claim keys are case-sensitive, so populate each one explicitly (including denials set to false)", missing)
}
