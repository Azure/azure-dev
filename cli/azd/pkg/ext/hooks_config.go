// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import "fmt"

// HooksConfig is an alias for map of hook names to slices of hook configurations.
// It supports unmarshalling both legacy single-hook and newer multi-hook formats.
type HooksConfig map[string][]*HookConfig

// UnmarshalYAML converts hook configuration from YAML, supporting both single-hook configuration
// and multiple-hooks configuration.
func (ch *HooksConfig) UnmarshalYAML(unmarshal func(any) error) error {
	var legacyConfig map[string]*HookConfig

	if err := unmarshal(&legacyConfig); err == nil {
		newConfig := HooksConfig{}

		for key, value := range legacyConfig {
			newConfig[key] = []*HookConfig{value}
		}

		*ch = newConfig
		return nil
	}

	var newConfig map[string][]*HookConfig
	if err := unmarshal(&newConfig); err != nil {
		return fmt.Errorf("failed to unmarshal hooks configuration: %w", err)
	}

	*ch = newConfig

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
