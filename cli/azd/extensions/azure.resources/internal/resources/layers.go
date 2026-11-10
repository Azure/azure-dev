// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"fmt"
	"strings"
)

// ValidateProjectLayers prevents entries from sharing the stable SDK's single resources provider instance.
// A nonempty selectedLayer also checks the resources layer proposed by the add wizard.
func ValidateProjectLayers(project map[string]any, selectedLayer string) error {
	var entries []string
	inspectInfra := func(infra map[string]any, prefix string) error {
		provider, _ := infra["provider"].(string)
		raw, layered := infra["layers"]
		if !layered {
			if provider == ProviderName {
				entries = append(entries, prefix)
			}
			return nil
		}
		layers, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s.layers must be a sequence", prefix)
		}
		for i, raw := range layers {
			layer, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.layers[%d] must be a mapping", prefix, i)
			}
			name, _ := layer["name"].(string)
			effective := provider
			if explicit, exists := layer["provider"]; exists {
				effective, ok = explicit.(string)
				if !ok {
					return fmt.Errorf("%s layer %q provider must be a string", prefix, name)
				}
			}
			if prefix == "infra" && selectedLayer != "" && name == selectedLayer {
				continue
			}
			if effective == ProviderName {
				entries = append(entries, prefix+"."+name)
			}
		}
		return nil
	}
	if infra, ok := project["infra"].(map[string]any); ok {
		if err := inspectInfra(infra, "infra"); err != nil {
			return err
		}
	}
	if raw, exists := project["layers"]; exists {
		layers, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("project layers must be a sequence")
		}
		for i, raw := range layers {
			layer, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("project layers[%d] must be a mapping", i)
			}
			rawInfra, exists := layer["infra"]
			if !exists {
				continue
			}
			infraEntries, ok := rawInfra.([]any)
			if !ok {
				return fmt.Errorf("project layers[%d].infra must be a sequence", i)
			}
			name, _ := layer["name"].(string)
			for j, rawEntry := range infraEntries {
				infra, ok := rawEntry.(map[string]any)
				if !ok {
					return fmt.Errorf("project layers[%d].infra[%d] must be a mapping", i, j)
				}
				if err := inspectInfra(infra, fmt.Sprintf("layers.%s.infra[%d]", name, j)); err != nil {
					return err
				}
			}
		}
	}
	if selectedLayer != "" {
		entries = append(entries, "infra."+selectedLayer)
	}
	if len(entries) > 1 {
		return fmt.Errorf("only one resources infrastructure entry is supported; found %s; "+
			"combine resource declarations into one resources layer", strings.Join(entries, ", "))
	}
	return nil
}
