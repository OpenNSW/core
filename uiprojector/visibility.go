package uiprojector

import (
	"strings"
)

// ShouldRender implements generic visibility logic.
func ShouldRender(section SectionBlueprint, facts Facts) bool {
	if section.VisibleWhen == nil {
		return true
	}

	rules := section.VisibleWhen

	// State-based visibility
	if len(rules.States) > 0 {
		found := false
		for _, s := range rules.States {
			if strings.EqualFold(s, facts.State) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Data-existence visibility
	if rules.RequireDataKey != "" {
		val, exists := facts.Data[rules.RequireDataKey]
		if !exists || val == nil {
			return false
		}
	}

	return true
}
