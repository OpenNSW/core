// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package uiprojector_test

import (
	"testing"

	"github.com/OpenNSW/core/uiprojector"
	"github.com/stretchr/testify/assert"
)

func TestShouldRender(t *testing.T) {
	tests := []struct {
		name    string
		section uiprojector.SectionBlueprint
		facts   uiprojector.Facts
		want    bool
	}{
		{
			name:    "nil VisibleWhen renders by default",
			section: uiprojector.SectionBlueprint{},
			facts:   uiprojector.Facts{State: "ANY"},
			want:    true,
		},
		{
			name: "state matches one of allowed states",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{States: []string{"DRAFT", "IN_PROGRESS"}},
			},
			facts: uiprojector.Facts{State: "IN_PROGRESS"},
			want:  true,
		},
		{
			name: "state does not match any allowed state",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{States: []string{"DRAFT"}},
			},
			facts: uiprojector.Facts{State: "COMPLETED"},
			want:  false,
		},
		{
			name: "state comparison is case-insensitive",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{States: []string{"in_progress"}},
			},
			facts: uiprojector.Facts{State: "IN_PROGRESS"},
			want:  true,
		},
		{
			name: "empty VisibleWhen struct does not filter",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{},
			},
			facts: uiprojector.Facts{State: "ANYTHING"},
			want:  true,
		},
		{
			name: "RequireDataKey present with non-nil value renders",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{RequireDataKey: "approval"},
			},
			facts: uiprojector.Facts{Data: map[string]any{"approval": "yes"}},
			want:  true,
		},
		{
			name: "RequireDataKey present but nil hides",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{RequireDataKey: "approval"},
			},
			facts: uiprojector.Facts{Data: map[string]any{"approval": nil}},
			want:  false,
		},
		{
			name: "RequireDataKey absent hides",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{RequireDataKey: "approval"},
			},
			facts: uiprojector.Facts{Data: map[string]any{}},
			want:  false,
		},
		{
			name: "states and RequireDataKey both pass renders",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{
					States:         []string{"APPROVED"},
					RequireDataKey: "approval",
				},
			},
			facts: uiprojector.Facts{
				State: "APPROVED",
				Data:  map[string]any{"approval": "yes"},
			},
			want: true,
		},
		{
			name: "state fails even when data key present",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{
					States:         []string{"APPROVED"},
					RequireDataKey: "approval",
				},
			},
			facts: uiprojector.Facts{
				State: "DRAFT",
				Data:  map[string]any{"approval": "yes"},
			},
			want: false,
		},
		{
			name: "data key missing even when state passes",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{
					States:         []string{"APPROVED"},
					RequireDataKey: "approval",
				},
			},
			facts: uiprojector.Facts{
				State: "APPROVED",
				Data:  map[string]any{},
			},
			want: false,
		},
		// RequireClaim cases
		{
			name: "claim present and true renders",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{RequireClaim: "can_approve"},
			},
			facts: uiprojector.Facts{
				Claims: map[string]bool{"can_approve": true},
			},
			want: true,
		},
		{
			name: "claim present but false hides",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{RequireClaim: "can_approve"},
			},
			facts: uiprojector.Facts{
				Claims: map[string]bool{"can_approve": false},
			},
			want: false,
		},
		{
			name: "claim absent from map hides",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{RequireClaim: "can_approve"},
			},
			facts: uiprojector.Facts{
				Claims: map[string]bool{"is_owner": true},
			},
			want: false,
		},
		{
			name: "nil Claims map hides when claim required",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{RequireClaim: "can_approve"},
			},
			facts: uiprojector.Facts{},
			want:  false,
		},
		{
			name: "empty RequireClaim is a no-op",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{RequireClaim: ""},
			},
			facts: uiprojector.Facts{},
			want:  true,
		},
		{
			name: "state passes and claim true renders",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{
					States:       []string{"PENDING_REVIEW"},
					RequireClaim: "can_approve",
				},
			},
			facts: uiprojector.Facts{
				State:  "PENDING_REVIEW",
				Claims: map[string]bool{"can_approve": true},
			},
			want: true,
		},
		{
			name: "state passes but claim false hides",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{
					States:       []string{"PENDING_REVIEW"},
					RequireClaim: "can_approve",
				},
			},
			facts: uiprojector.Facts{
				State:  "PENDING_REVIEW",
				Claims: map[string]bool{"can_approve": false},
			},
			want: false,
		},
		{
			name: "claim true but state fails hides",
			section: uiprojector.SectionBlueprint{
				VisibleWhen: &uiprojector.VisibleWhen{
					States:       []string{"PENDING_REVIEW"},
					RequireClaim: "can_approve",
				},
			},
			facts: uiprojector.Facts{
				State:  "DRAFT",
				Claims: map[string]bool{"can_approve": true},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uiprojector.ShouldRender(tt.section, tt.facts)
			assert.Equal(t, tt.want, got)
		})
	}
}
