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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uiprojector.ShouldRender(tt.section, tt.facts)
			assert.Equal(t, tt.want, got)
		})
	}
}
