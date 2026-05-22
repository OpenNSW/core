package notifications

import (
	"encoding/json"
	"fmt"
	"os"
)

func loadConfigMap(path string) (map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read notification config %q: %w", path, err)
	}
	var cfgMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cfgMap); err != nil {
		return nil, fmt.Errorf("parse notification config: %w", err)
	}
	return cfgMap, nil
}
