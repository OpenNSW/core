package orchestrator

// setNestedKey sets a value in a map using a dot-separated path.
func setNestedKey(m map[string]any, dotPath string, value any) {
	if m == nil || dotPath == "" {
		return
	}
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
	m[dotPath] = value
}
