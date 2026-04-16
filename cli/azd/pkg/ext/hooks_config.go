// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// HooksConfig is an alias for map of hook names to slices of hook configurations.
// It supports unmarshalling both legacy single-hook and newer multi-hook formats.
type HooksConfig map[string][]*HookConfig

// UnmarshalYAML converts hook configuration from YAML, supporting both single-hook configuration
// and multiple-hooks configuration.
//
// Each hook entry is independently parsed as either a single HookConfig (mapping node) or a list
// of HookConfigs (sequence node), allowing mixed formats within the same hooks: block.
func (ch *HooksConfig) UnmarshalYAML(unmarshal func(any) error) error {
	// Unmarshal into map[string]any so each value retains its Go representation:
	//   YAML mapping  → map[string]any
	//   YAML sequence → []any
	//   YAML null     → nil
	var raw map[string]any
	if err := unmarshal(&raw); err != nil {
		return fmt.Errorf("failed to unmarshal hooks configuration: %w", err)
	}

	result := make(HooksConfig, len(raw))

	for key, val := range raw {
		switch val.(type) {
		case nil:
			// A null YAML value (e.g. "preprovision:" with no body).
			// Preserve with a nil entry so downstream validation can report it.
			result[key] = []*HookConfig{nil}

		case map[string]any:
			// Single hook configuration (a YAML mapping).
			encoded, encErr := yaml.Marshal(val)
			if encErr != nil {
				return fmt.Errorf("failed to unmarshal hook %q: %w", key, encErr)
			}

			var single HookConfig
			if err := yaml.Unmarshal(encoded, &single); err != nil {
				return fmt.Errorf("failed to unmarshal hook %q: %w", key, err)
			}

			result[key] = []*HookConfig{&single}

		case []any:
			// List of hook configurations (a YAML sequence).
			encoded, encErr := yaml.Marshal(val)
			if encErr != nil {
				return fmt.Errorf("failed to unmarshal hook %q: %w", key, encErr)
			}

			var list []*HookConfig
			if err := yaml.Unmarshal(encoded, &list); err != nil {
				return fmt.Errorf("failed to unmarshal hook %q: %w", key, err)
			}

			result[key] = list

		default:
			return fmt.Errorf(
				"failed to unmarshal hook %q: expected mapping or sequence, got %T",
				key, val,
			)
		}
	}

	*ch = result

	return nil
}

// MarshalYAML marshals hook configuration to YAML, supporting both single-hook configuration
// and multiple-hooks configuration.
func (ch HooksConfig) MarshalYAML() (any, error) {
	if len(ch) == 0 {
		return nil, nil
	}

	result := map[string]any{}
	for key, hooks := range ch {
		if len(hooks) == 1 {
			result[key] = hooks[0]
		} else {
			result[key] = hooks
		}
	}

	return result, nil
}
