// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"errors"
	"fmt"
	"strings"
)

// Project configuration field names.
const (
	FieldName      = "name"
	FieldProvider  = "provider"
	FieldInfra     = "infra"
	FieldLayers    = "layers"
	FieldConfig    = "config"
	FieldResources = "resources"
	FieldServices  = "services"
)

// ProjectLayer holds the layer fields used by the resources extension and its original mapping.
type ProjectLayer struct {
	Name     string
	Provider string
	Raw      map[string]any
}

// UnmarshalProjectLayer reads known fields and retains the original mapping for round-tripping.
func UnmarshalProjectLayer(value any) (ProjectLayer, error) {
	values, ok := value.(map[string]any)
	if !ok {
		return ProjectLayer{}, errors.New("must be a map[string]any")
	}

	name, err := getScalarValue[string](values, FieldName)
	if err != nil {
		return ProjectLayer{}, err
	}
	provider, err := getScalarValue[string](values, FieldProvider)
	if err != nil {
		return ProjectLayer{}, fmt.Errorf("layer %q provider: %w", name, err)
	}

	return ProjectLayer{Name: name, Provider: provider, Raw: values}, nil
}

// ValidateProjectLayers checks infrastructure layer names and prevents entries from sharing
// the stable SDK's single resources provider instance.
// A nonempty selectedLayer also checks the resources layer proposed by the add wizard.
func ValidateProjectLayers(project map[string]any, selectedLayer string) error {
	var entries []string
	inspectInfra := func(infra map[string]any, prefix string) error {
		raw, layered := infra[FieldLayers]
		if !layered {
			provider, err := getScalarValue[string](infra, FieldProvider)
			if err != nil {
				return fmt.Errorf("%s %w", prefix, err)
			}
			if provider == ProviderName {
				entries = append(entries, prefix)
			}
			return nil
		}
		layers, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s.layers must be a sequence", prefix)
		}
		names := map[string]bool{}
		for i, raw := range layers {
			layer, err := UnmarshalProjectLayer(raw)
			if err != nil {
				return fmt.Errorf("%s.layers[%d] %w", prefix, i, err)
			}
			if layer.Name == "" || names[layer.Name] {
				return fmt.Errorf("%s.layers[%d] requires a unique, nonempty name", prefix, i)
			}
			names[layer.Name] = true

			if prefix == FieldInfra && selectedLayer != "" && layer.Name == selectedLayer {
				continue
			}
			if layer.Provider == ProviderName {
				entries = append(entries, prefix+"."+layer.Name)
			}
		}
		return nil
	}
	if infra, ok := project[FieldInfra].(map[string]any); ok {
		if err := inspectInfra(infra, FieldInfra); err != nil {
			return err
		}
	}
	if raw, exists := project[FieldLayers]; exists {
		layers, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("project layers must be a sequence")
		}
		for i, raw := range layers {
			layer, err := UnmarshalProjectLayer(raw)
			if err != nil {
				return fmt.Errorf("project layers[%d] %w", i, err)
			}
			rawInfra, exists := layer.Raw[FieldInfra]
			if !exists {
				continue
			}
			infraEntries, ok := rawInfra.([]any)
			if !ok {
				return fmt.Errorf("project layers[%d].infra must be a sequence", i)
			}
			for j, rawEntry := range infraEntries {
				infra, ok := rawEntry.(map[string]any)
				if !ok {
					return fmt.Errorf("project layers[%d].infra[%d] must be a mapping", i, j)
				}
				if err := inspectInfra(infra, fmt.Sprintf("layers.%s.infra[%d]", layer.Name, j)); err != nil {
					return err
				}
			}
		}
	}
	if selectedLayer != "" {
		entries = append(entries, FieldInfra+"."+selectedLayer)
	}
	if len(entries) > 1 {
		return fmt.Errorf("only one resources infrastructure entry is supported; found %s; "+
			"combine resource declarations into one resources layer", strings.Join(entries, ", "))
	}
	return nil
}
