// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package maputil

import (
	"github.com/OpenNSW/core/internal/deepcopy"
)

// GetNestedKey retrieves a value from a nested map using a dot-separated path.
// Returns the value and a boolean indicating whether it was found.
// e.g. GetNestedKey(m, "userform.applicant_name") returns (m["userform"]["applicant_name"], true)
func GetNestedKey(m map[string]any, dotPath string) (any, bool) {
	if m == nil || dotPath == "" {
		return nil, false
	}

	// Find the first dot
	for i := 0; i < len(dotPath); i++ {
		if dotPath[i] == '.' {
			key := dotPath[:i]
			rest := dotPath[i+1:]
			sub, ok := m[key]
			if !ok || sub == nil {
				return nil, false
			}
			subMap, ok := sub.(map[string]any)
			if !ok {
				return nil, false
			}
			return GetNestedKey(subMap, rest)
		}
	}

	// No dot found — leaf key
	val, ok := m[dotPath]
	return val, ok
}

// SetNestedKey sets a value in a map using a dot-separated path.
// e.g. SetNestedKey(m, "userform.applicant_name", "Acme") sets m["userform"]["applicant_name"] = "Acme"
// If the target key and the incoming value are both map[string]any, they are recursively merged.
// Assigned values (like nested maps/slices) are deep-copied.
func SetNestedKey(m map[string]any, dotPath string, value any) {
	if m == nil || dotPath == "" {
		return
	}
	// Find the first dot
	for i := 0; i < len(dotPath); i++ {
		if dotPath[i] == '.' {
			key := dotPath[:i]
			rest := dotPath[i+1:]
			sub, ok := m[key]
			var subMap map[string]any
			if !ok || sub == nil {
				subMap = make(map[string]any)
			} else if existingMap, ok := sub.(map[string]any); ok {
				subMap = deepcopy.Map(existingMap)
			} else {
				subMap = make(map[string]any)
			}
			SetNestedKey(subMap, rest, value)
			m[key] = subMap
			return
		}
	}

	// No dot found — leaf key.
	// If the existing value is a map[string]any and the new value is a map[string]any,
	// perform a deep merge to avoid overwriting existing fields (e.g. userinfo.reference_number).
	if existingVal, exists := m[dotPath]; exists {
		if existingMap, ok := existingVal.(map[string]any); ok {
			if incomingMap, ok := value.(map[string]any); ok {
				newMap := deepcopy.Map(existingMap)
				mergeMaps(newMap, incomingMap)
				m[dotPath] = newMap
				return
			}
		}
	}

	// Otherwise, overwrite/set the deep-copied value.
	m[dotPath] = deepcopy.Value(value)
}

// mergeMaps recursively merges src into dst. Values from src are deep-copied.
func mergeMaps(dst, src map[string]any) {
	for k, v := range src {
		if srcMap, ok := v.(map[string]any); ok {
			if dstSub, exists := dst[k]; exists {
				if dstSubMap, ok := dstSub.(map[string]any); ok {
					mergeMaps(dstSubMap, srcMap)
					continue
				}
			}
			dst[k] = deepcopy.Value(srcMap)
		} else {
			dst[k] = deepcopy.Value(v)
		}
	}
}
