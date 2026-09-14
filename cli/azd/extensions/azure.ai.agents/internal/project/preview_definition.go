// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"go.yaml.in/yaml/v3"
)

// AgentPreviewDefinition combines the authored definition and its companion
// manifest without changing either file.
type AgentPreviewDefinition struct {
	Definition   agent_yaml.ContainerAgent
	Sources      []string
	alternatives []previewDefinitionAlternative
}

type previewDefinitionAlternative struct {
	source     string
	definition agent_yaml.ContainerAgent
}

type previewDefinitionSource struct {
	path               string
	properties         map[string]any
	overridden         []previewDefinitionSource
	replaceEnvironment bool
}

// LoadAgentPreviewDefinition reads a standalone definition or manifest together
// with its companion files. The explicitly selected file has precedence.
func LoadAgentPreviewDefinition(path string, environment map[string]string) (*AgentPreviewDefinition, error) {
	if strings.TrimSpace(path) == "" {
		path = "agent.yaml"
	}
	primary, err := readPreviewDefinitionSource(path, environment)
	if err != nil {
		return nil, err
	}
	sources, err := companionPreviewSources(filepath.Dir(path), path, environment)
	if err != nil {
		return nil, err
	}
	sources = append(sources, primary)
	return combinePreviewDefinitionSources(sources)
}

func companionPreviewSources(
	directory, selected string, environment map[string]string,
) ([]previewDefinitionSource, error) {
	var sources []previewDefinitionSource
	// Materialized definitions override template defaults; the selected input
	// overrides both. Overlapping differences are retained for the preview.
	for _, names := range [][]string{
		{"agent.manifest.yaml", "agent.manifest.yml"},
		{"agent.yaml", "agent.yml"},
	} {
		for _, name := range names {
			path := filepath.Join(directory, name)
			if samePreviewPath(path, selected) {
				continue
			}
			_, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("inspect agent preview source %q: %w", path, err)
			}
			source, err := readPreviewDefinitionSource(path, environment)
			if err != nil {
				return nil, err
			}
			sources = append(sources, source)
		}
	}
	return sources, nil
}

func samePreviewPath(a, b string) bool {
	if b == "" {
		return false
	}
	first, errA := filepath.Abs(a)
	second, errB := filepath.Abs(b)
	return errA == nil && errB == nil && first == second
}

func readPreviewDefinitionSource(path string, environment map[string]string) (previewDefinitionSource, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Reading the selected definition and named companions is intentional.
	if err != nil {
		return previewDefinitionSource{}, exterrors.Dependency(
			exterrors.CodeAgentDefinitionNotFound,
			fmt.Sprintf("failed to read agent definition %q: %s", path, err),
			"check the definition path and file permissions",
		)
	}
	var properties map[string]any
	if err := yaml.Unmarshal(data, &properties); err != nil {
		return previewDefinitionSource{}, exterrors.Validation(exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("parse agent preview source %q: %s", path, err), "fix the YAML syntax")
	}
	if len(properties) == 0 {
		return previewDefinitionSource{}, fmt.Errorf("agent preview source %q is empty", path)
	}
	parameters := map[string]any{}
	var overridden []previewDefinitionSource
	if _, hasTemplate := properties["template"]; hasTemplate {
		manifest, err := agent_yaml.LoadAndValidateAgentManifest(data)
		if err != nil {
			return previewDefinitionSource{}, fmt.Errorf("load manifest %q: %w", path, err)
		}
		for _, parameter := range manifest.Parameters.Properties {
			if parameter.Default != nil {
				parameters[parameter.Name] = *parameter.Default
			}
		}
		template, ok := properties["template"].(map[string]any)
		if !ok {
			return previewDefinitionSource{}, fmt.Errorf("manifest %q template must be a mapping", path)
		}
		// ExtractAgentDefinition is the shared manifest metadata inheritance rule.
		definition, ok := manifest.Template.(agent_yaml.ContainerAgent)
		if !ok {
			return previewDefinitionSource{}, fmt.Errorf("manifest %q must describe a hosted agent", path)
		}
		rootMetadata, _ := properties["metadata"].(map[string]any)
		rootCommon := map[string]any{}
		if _, hasTemplateMetadata := template["metadata"]; hasTemplateMetadata && rootMetadata != nil {
			rootCommon["metadata"] = rootMetadata
		}
		if description, exists := properties["description"]; exists && template["description"] != nil {
			rootCommon["description"] = description
		}
		if len(rootCommon) > 0 {
			resolvedRoot, err := resolvePreviewProperties(rootCommon, environment, parameters)
			if err != nil {
				return previewDefinitionSource{}, err
			}
			overridden = append(overridden, previewDefinitionSource{
				path: path + "#manifest", properties: resolvedRoot,
			})
		}
		properties = maps.Clone(template)
		if _, exists := properties["name"]; !exists {
			properties["name"] = definition.Name
		}
		if _, exists := properties["description"]; !exists && definition.Description != nil {
			properties["description"] = *definition.Description
		}
		if definition.Metadata != nil {
			properties["metadata"] = mergePreviewProperties(rootMetadata, *definition.Metadata)
		}
	}
	resolved, err := resolvePreviewProperties(properties, environment, parameters)
	if err != nil {
		return previewDefinitionSource{}, fmt.Errorf("resolve agent preview source %q: %w", path, err)
	}
	return previewDefinitionSource{path: path, properties: resolved, overridden: overridden}, nil
}

func resolvePreviewProperties(
	properties map[string]any, environment map[string]string, parameters map[string]any,
) (map[string]any, error) {
	lookup := func(name string) (string, bool) {
		name = strings.TrimPrefix(strings.TrimSpace(name), "param.")
		if value, found := environment[name]; found {
			return value, true
		}
		if value, found := os.LookupEnv(name); found {
			return value, true
		}
		if value, found := parameters[name]; found {
			return fmt.Sprint(value), true
		}
		return "", false
	}
	var resolve func(any) (any, error)
	resolve = func(value any) (any, error) {
		switch value := value.(type) {
		case string:
			original := value
			value = agent_yaml.PlaceholderPattern.ReplaceAllStringFunc(value, func(token string) string {
				if strings.Contains(original, "$"+token) {
					return token // Foundry server-side expressions are not manifest parameters.
				}
				match := agent_yaml.PlaceholderPattern.FindStringSubmatch(token)
				if replacement, found := lookup(match[1]); found {
					return replacement
				}
				name := strings.TrimPrefix(strings.TrimSpace(match[1]), "param.")
				if environmentVariableNamePattern.MatchString(name) {
					return "${" + name + "}"
				}
				return token
			})
			var missing []string
			expanded, err := ExpandEnv(value, func(name string) string {
				replacement, found := lookup(name)
				if !found {
					missing = append(missing, name)
				}
				return replacement
			})
			if err != nil {
				return nil, err
			}
			for _, name := range missing {
				if strings.Contains(value, "${"+name+"}") || strings.Contains(value, "$"+name) {
					return value, nil
				}
			}
			return expanded, nil
		case map[string]any:
			result := make(map[string]any, len(value))
			for key, item := range value {
				resolved, err := resolve(item)
				if err != nil {
					return nil, fmt.Errorf("resolve field %q: %w", key, err)
				}
				result[key] = resolved
			}
			return result, nil
		case []any:
			result := make([]any, len(value))
			for i, item := range value {
				resolved, err := resolve(item)
				if err != nil {
					return nil, err
				}
				result[i] = resolved
			}
			return result, nil
		default:
			return value, nil
		}
	}
	resolved, err := resolve(properties)
	if err != nil {
		return nil, err
	}
	result, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("resolved agent definition must be a mapping")
	}
	return result, nil
}

func mergePreviewProperties(base, override map[string]any) map[string]any {
	result := maps.Clone(base)
	if result == nil {
		result = map[string]any{}
	}
	for key, value := range override {
		if object, ok := value.(map[string]any); ok && len(object) > 0 {
			previous, _ := result[key].(map[string]any)
			result[key] = mergePreviewProperties(previous, object)
		} else {
			result[key] = value
		}
	}
	return result
}

func mergePreviewDefinitionProperties(base, override map[string]any) (map[string]any, error) {
	result := mergePreviewProperties(base, override)
	entries, hasEntries := override["environment_variables"].([]any)
	if !hasEntries || len(entries) == 0 {
		return result, nil
	}
	var defaults []any
	if previous := base["environment_variables"]; previous != nil {
		var ok bool
		defaults, ok = previous.([]any)
		if !ok {
			return nil, fmt.Errorf("environment_variables must be a list")
		}
	}
	byName := map[string]any{}
	for _, source := range [][]any{defaults, entries} {
		seen := map[string]bool{}
		for i, item := range source {
			entry, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("environment_variables[%d] must be a mapping", i)
			}
			name, ok := entry["name"].(string)
			if !ok || !environmentVariableNamePattern.MatchString(name) {
				return nil, fmt.Errorf("environment_variables[%d] requires a valid variable name", i)
			}
			if seen[name] {
				return nil, fmt.Errorf("environment_variables contains duplicate name %q", name)
			}
			seen[name] = true
			byName[name] = item
		}
	}
	merged := make([]any, 0, len(byName))
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		merged = append(merged, byName[name])
	}
	result["environment_variables"] = merged
	return result, nil
}

func combinePreviewDefinitionSources(sources []previewDefinitionSource) (*AgentPreviewDefinition, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("no agent definition sources found")
	}
	combined := map[string]any{}
	result := &AgentPreviewDefinition{}
	for _, source := range sources {
		if source.replaceEnvironment {
			delete(combined, "environment_variables")
		}
		var err error
		combined, err = mergePreviewDefinitionProperties(combined, source.properties)
		if err != nil {
			return nil, fmt.Errorf("merge agent definition from %q: %w", source.path, err)
		}
		result.Sources = append(result.Sources, source.path)
	}
	definition, err := parsePreviewProperties(combined)
	if err != nil {
		return nil, err
	}
	result.Definition = definition
	alternatives := slices.Clone(sources[:len(sources)-1])
	for _, source := range sources {
		alternatives = append(alternatives, source.overridden...)
	}
	for _, source := range alternatives {
		properties, err := mergePreviewDefinitionProperties(combined, source.properties)
		if err != nil {
			return nil, fmt.Errorf("merge alternative settings from %q: %w", source.path, err)
		}
		candidate, err := parsePreviewProperties(properties)
		if err != nil {
			return nil, fmt.Errorf("parse alternative settings from %q: %w", source.path, err)
		}
		result.alternatives = append(result.alternatives, previewDefinitionAlternative{
			source: source.path, definition: candidate,
		})
	}
	return result, nil
}

func parsePreviewProperties(properties map[string]any) (agent_yaml.ContainerAgent, error) {
	unknownImage := ""
	if image, ok := properties["image"].(string); ok && previewContainsUnknown(image) {
		unknownImage = image
		properties = maps.Clone(properties)
		delete(properties, "image")
	}
	data, err := yaml.Marshal(properties)
	if err != nil {
		return agent_yaml.ContainerAgent{}, fmt.Errorf("marshal resolved agent definition: %w", err)
	}
	definition, hosted, err := parseContainerAgentYAML(data)
	if err != nil {
		return agent_yaml.ContainerAgent{}, err
	}
	if !hosted {
		return agent_yaml.ContainerAgent{}, exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind, "deployment preview supports hosted agents only", "use kind: hosted",
		)
	}
	if unknownImage != "" {
		definition.Image = unknownImage
	}
	return definition, nil
}

func normalizePreviewEnvironment(
	definition *agent_yaml.ContainerAgent, overrides map[string]string, preserveUnresolved bool,
) (map[string]string, error) {
	environment := maps.Clone(overrides)
	if environment == nil {
		environment = map[string]string{}
	}
	if definition.EnvironmentVariables != nil {
		for _, variable := range *definition.EnvironmentVariables {
			if _, found := environment[variable.Name]; found {
				continue
			}
			value := variable.Value
			if !preserveUnresolved {
				var err error
				value, err = ResolveAgentEnvironmentVariable(variable.Name, value, nil, os.Getenv)
				if err != nil {
					return nil, err
				}
			}
			environment[variable.Name] = value
		}
	}
	if definition.Toolbox != nil {
		environment["TOOLBOX_NAME"] = strings.TrimSpace(definition.Toolbox.Name)
		if version := strings.TrimSpace(definition.Toolbox.Version); version != "" {
			environment["TOOLBOX_VERSION"] = version
		}
	}
	return environment, nil
}

func previewContainsUnknown(value any) bool {
	switch value := value.(type) {
	case string:
		return strings.Contains(value, "${") || len(agent_yaml.ExtractUnresolvedPlaceholders(value)) > 0
	case []any:
		return slices.ContainsFunc(value, previewContainsUnknown)
	case map[string]any:
		for _, item := range value {
			if previewContainsUnknown(item) {
				return true
			}
		}
	}
	return false
}
