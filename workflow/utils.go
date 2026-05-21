package engine

import "strings"

// parseMappingKey strips a trailing "?" from a mapping key and reports whether
// the entry should be treated as optional. A trailing "?" marks the entry as
// optional, meaning the engine will skip the mapping silently when the source
// key is absent instead of failing the workflow.
func parseMappingKey(rawKey string) (key string, optional bool) {
	if strings.HasSuffix(rawKey, "?") {
		return rawKey[:len(rawKey)-1], true
	}
	return rawKey, false
}

// getNestedKey retrieves a value from a nested map using a dot-separated path.
// Returns the value and a boolean indicating whether it was found.
// e.g. getNestedKey(m, "userform.applicant_name") returns (m["userform"]["applicant_name"], true)
func getNestedKey(m map[string]any, dotPath string) (any, bool) {
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
			return getNestedKey(subMap, rest)
		}
	}

	// No dot found — leaf key
	val, ok := m[dotPath]
	return val, ok
}

// setNestedKey sets a value in a map using a dot-separated path.
// e.g. setNestedKey(m, "userform.applicant_name", "Acme") sets m["userform"]["applicant_name"] = "Acme"
func setNestedKey(m map[string]any, dotPath string, value any) {
	if m == nil || dotPath == "" {
		return
	}
	// Find the first dot
	for i := 0; i < len(dotPath); i++ {
		if dotPath[i] == '.' {
			key := dotPath[:i]
			rest := dotPath[i+1:]
			sub, ok := m[key]
			if !ok || sub == nil {
				sub = make(map[string]any)
			}
			subMap, ok := sub.(map[string]any)
			if !ok {
				subMap = make(map[string]any)
			}
			setNestedKey(subMap, rest, value)
			m[key] = subMap
			return
		}
	}
	// No dot found — leaf key
	m[dotPath] = value
}
