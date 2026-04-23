// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"encoding/json"
	"fmt"
)

// UnmarshalHookConfig unmarshals the raw config map into a typed struct.
// Returns a zero-value T if config is nil or empty.
// Uses JSON re-marshal for deterministic conversion with proper type checking.
func UnmarshalHookConfig[T any](config map[string]any) (T, error) {
	var result T

	if len(config) == 0 {
		return result, nil
	}

	data, err := json.Marshal(config)
	if err != nil {
		return result, fmt.Errorf("marshalling hook config to JSON: %w", err)
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("unmarshalling hook config: %w", err)
	}

	return result, nil
}
