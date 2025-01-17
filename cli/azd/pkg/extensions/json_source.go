package extensions

import (
	"encoding/json"
	"fmt"
)

func newJsonSource(name string, jsonRegistry string) (Source, error) {
	var registry *Registry
	err := json.Unmarshal([]byte(jsonRegistry), &registry)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal extensions JSON %w", err)
	}

	return newRegistrySource(name, registry)
}
