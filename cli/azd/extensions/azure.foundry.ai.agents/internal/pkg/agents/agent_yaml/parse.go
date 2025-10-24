// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// LoadAndValidateAgentManifest parses YAML content and validates it as an AgentManifest
// Returns the parsed manifest and any validation errors
func LoadAndValidateAgentManifest(yamlContent []byte) (*AgentManifest, error) {
	var manifest AgentManifest
	if err := yaml.Unmarshal(yamlContent, &manifest); err != nil {
		return nil, fmt.Errorf("YAML content does not conform to AgentManifest format: %w", err)
	}

	if err := ValidateAgentManifest(&manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// ValidateAgentManifest performs basic validation of an AgentManifest
// Returns an error if the manifest is invalid, nil if valid
func ValidateAgentManifest(manifest *AgentManifest) error {
	var errors []string

	// First, extract the kind from the template to determine the agent type
	templateMap, ok := manifest.Template.(map[string]interface{})
	if !ok {
		errors = append(errors, "template must be a valid object")
	} else {
		kindValue, hasKind := templateMap["kind"]
		if !hasKind {
			errors = append(errors, "template.kind is required")
		} else {
			kind, kindOk := kindValue.(string)
			if !kindOk {
				errors = append(errors, "template.kind must be a string")
			} else {
				// Validate the kind is supported
				if !IsValidAgentKind(AgentKind(kind)) {
					validKinds := ValidAgentKinds()
					validKindStrings := make([]string, len(validKinds))
					for i, validKind := range validKinds {
						validKindStrings[i] = string(validKind)
					}
					errors = append(errors, fmt.Sprintf("template.kind must be one of: %v, got '%s'", validKindStrings, kind))
				} else {
					// Convert template to YAML bytes and unmarshal to specific type based on kind
					templateBytes, err := yaml.Marshal(manifest.Template)
					if err != nil {
						errors = append(errors, "failed to process template structure")
					} else {
						switch AgentKind(kind) {
						case AgentKindPrompt:
							var agent PromptAgent
							if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
								if agent.Name == "" {
									errors = append(errors, "template.name is required")
								}
								if agent.Model.Id == "" {
									errors = append(errors, "template.model.id is required")
								}
							}
						case AgentKindHosted:
							var agent HostedContainerAgent
							if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
								if agent.Name == "" {
									errors = append(errors, "template.name is required")
								}
								if len(agent.Models) == 0 {
									errors = append(errors, "template.models is required and must not be empty")
								}
							}
						case AgentKindContainerApp, AgentKindYamlContainerApp:
							var agent ContainerAgent
							if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
								if agent.Name == "" {
									errors = append(errors, "template.name is required")
								}
								if len(agent.Models) == 0 {
									errors = append(errors, "template.models is required and must not be empty")
								}
							}
						case AgentKindWorkflow:
							var agent WorkflowAgent
							if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
								if agent.Name == "" {
									errors = append(errors, "template.name is required")
								}
								// WorkflowAgent doesn't have models, so no model validation needed
							}
						}
					}
				}
			}
		}
	}

	if len(errors) > 0 {
		errorMsg := "validation failed:"
		for _, err := range errors {
			errorMsg += fmt.Sprintf("\n  - %s", err)
		}
		return fmt.Errorf("%s", errorMsg)
	}

	return nil
}
