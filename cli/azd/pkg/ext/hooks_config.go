// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import "fmt"

// HooksConfig is a map of hook names to slice of hook configurations.
// This type supports YAML unmarshalling of both the legacy single hook configuration format
// and the newer multiple hook configuration format.
type HooksConfig map[string][]*HookConfig

// UnmarshalYAML converts the hooks configuration from YAML supporting both legacy single hook configurations
// and new multiple hook configurations
func (ch *HooksConfig) UnmarshalYAML(unmarshal func(any) error) error {
	var legacyConfig map[string]*HookConfig

	// Attempt to unmarshal the legacy single hook configuration
	if err := unmarshal(&legacyConfig); err == nil {
		newConfig := HooksConfig{}

		for key, value := range legacyConfig {
			newConfig[key] = []*HookConfig{value}
		}

		*ch = newConfig
	} else { // Unmarshal the new multiple hook configuration
		var newConfig map[string][]*HookConfig
		if err := unmarshal(&newConfig); err != nil {
			return fmt.Errorf("failed to unmarshal hooks configuration: %w", err)
		}

		*ch = newConfig
	}

	return nil
}

// MarshalYAML marshals the hooks configuration to YAML supporting both legacy single hook configurations
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
