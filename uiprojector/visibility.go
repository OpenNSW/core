// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

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

	// Claim-based visibility: the caller pre-resolves AuthZ into Facts.Claims
	// before calling Assemble. The library only checks key presence and truth;
	// it makes no policy decisions of its own.
	if rules.RequireClaim != "" && !facts.Claims[rules.RequireClaim] {
		return false
	}

	return true
}
