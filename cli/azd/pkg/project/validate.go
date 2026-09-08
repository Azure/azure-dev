// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

// ConfigValidationError is returned when the azure.yaml configuration contains
// structural problems such as nil service, resource, or hook definitions.
// Callers can use [errors.As] to programmatically inspect the individual Issues.
type ConfigValidationError struct {
	Issues []string
}

func (e *ConfigValidationError) Error() string {
	return fmt.Sprintf("azure.yaml contains invalid configuration:\n  - %s", strings.Join(e.Issues, "\n  - "))
}

// validateParsedConfig checks a freshly-parsed ProjectConfig for nil service, resource, and
// hook definitions that would cause nil pointer dereference panics during subsequent processing.
// YAML sections like "web:" with only a comment or whitespace unmarshal as nil map entries,
// which would cause nil pointer dereference panics during subsequent processing.
//
// The function validates Services, Resources, and Hooks at both project and service levels.
// All problems are collected and returned in a single error so the user can fix them at once.
func validateParsedConfig(config *ProjectConfig) error {
	var problems []string
	if config.Layers != nil && (len(config.Services) > 0 || config.infraPresent) {
		problems = append(problems, "'layers' cannot be combined with top-level 'infra' or 'services'")
	}

	for key, svc := range config.Services {
		if svc == nil {
			problems = append(problems, fmt.Sprintf(
				"service '%s' has an empty definition;"+
					" expected properties such as host, language, and project",
				key,
			))
			continue
		}

		if svc.Infra.Layers != nil {
			problems = append(problems, fmt.Sprintf("service '%s' infrastructure cannot declare layers", key))
		}
		problems = append(problems, validateHooks(svc.Hooks, "service '"+key+"'")...)
	}

	layerNames := make(map[string]struct{}, len(config.Layers))
	serviceNames := make(map[string]string)
	infraNames := make(map[string]string)
	for i, layer := range config.Layers {
		if layer == nil {
			problems = append(problems, fmt.Sprintf("layer entry %d has an empty definition", i+1))
			continue
		}
		if layer.Name == "" {
			problems = append(problems, fmt.Sprintf("layer entry %d has an empty name", i+1))
		} else if _, has := layerNames[layer.Name]; has {
			problems = append(problems, fmt.Sprintf("duplicate layer name '%s'", layer.Name))
		} else {
			layerNames[layer.Name] = struct{}{}
		}
		if len(layer.Infra) == 0 && len(layer.Services) == 0 {
			problems = append(problems, fmt.Sprintf("layer '%s' must contain infrastructure or services", layer.Name))
		}

		for _, infra := range layer.Infra {
			if owner, has := infraNames[infra.Name]; has {
				if owner == layer.Name {
					problems = append(problems,
						fmt.Sprintf("duplicate infrastructure entry '%s' in layer '%s'", infra.Name, layer.Name))
				} else {
					problems = append(problems, fmt.Sprintf(
						"infrastructure entry '%s' is defined in both layers '%s' and '%s'",
						infra.Name, owner, layer.Name))
				}
			} else {
				infraNames[infra.Name] = layer.Name
			}
		}

		for name, service := range layer.Services {
			if service == nil {
				problems = append(problems,
					fmt.Sprintf("layer '%s' service '%s' has an empty definition", layer.Name, name))
				continue
			}
			if owner, has := serviceNames[name]; has {
				problems = append(problems, fmt.Sprintf(
					"service '%s' is defined in both layers '%s' and '%s'", name, owner, layer.Name))
			} else {
				serviceNames[name] = layer.Name
			}
			if service.Infra.Layers != nil {
				problems = append(problems, fmt.Sprintf(
					"layer '%s' service '%s' infrastructure cannot declare layers", layer.Name, name))
			}
			problems = append(problems,
				validateHooks(service.Hooks, "layer '"+layer.Name+"' service '"+name+"'")...)
		}
	}

	for key, res := range config.Resources {
		if res == nil {
			problems = append(problems,
				fmt.Sprintf("resource '%s' has an empty definition; expected properties such as type", key))
		}
	}

	problems = append(problems, validateHooks(config.Hooks, "")...)

	if len(problems) > 0 {
		// Sort for deterministic output regardless of map iteration order.
		slices.Sort(problems)

		return &ConfigValidationError{Issues: problems}
	}

	return nil
}

// Validate checks a project configuration before it is persisted.
func (config *ProjectConfig) Validate() error {
	if err := validateParsedConfig(config); err != nil {
		return err
	}
	if err := config.Infra.Validate(); err != nil {
		return err
	}
	for _, layer := range config.Layers {
		for _, entry := range layer.Infra {
			if entry.Layers != nil {
				return fmt.Errorf(
					"layer %q infrastructure entry %q cannot declare nested layers",
					layer.Name,
					entry.Name,
				)
			}
			// NOTE: this is a new constraint - the previous layer provider assumed bicep.
			if entry.Provider == provisioning.NotSpecified {
				return fmt.Errorf(
					"layer %q infrastructure entry %q must specify a provider",
					layer.Name,
					entry.Name,
				)
			}
		}
		infra := provisioning.Options{Layers: layer.Infra}
		if err := infra.Validate(); err != nil {
			return fmt.Errorf("validating layer %q: %w", layer.Name, err)
		}
	}
	if config.Format() == ProjectFormatLayersV2 {
		if err := ValidateLayerGraph(config); err != nil {
			return fmt.Errorf("validating layer graph: %w", err)
		}
	}
	return nil
}

// validateHooks checks a HooksConfig for nil entries. When scope is non-empty it is
// prepended to each problem description to identify the parent (e.g., "service 'web'").
func validateHooks(hooks HooksConfig, scope string) []string {
	var problems []string

	prefix := ""
	if scope != "" {
		prefix = scope + " "
	}

	for hookName, hookList := range hooks {
		if hookList == nil {
			problems = append(problems, fmt.Sprintf(
				"%shook '%s' has an empty definition;"+
					" expected properties such as run or shell",
				prefix, hookName,
			))
			continue
		}

		for i, hook := range hookList {
			if hook == nil {
				problems = append(problems,
					fmt.Sprintf("%shook '%s' entry %d has an empty definition; expected properties such as run or shell",
						prefix, hookName, i+1))
			}
		}
	}

	return problems
}
