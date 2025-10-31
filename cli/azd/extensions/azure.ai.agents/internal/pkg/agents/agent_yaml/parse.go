// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// LoadAndValidateAgentManifest parses YAML content and validates it as an AgentManifest
// Returns the parsed manifest and any validation errors
func LoadAndValidateAgentManifest(manifestYamlContent []byte) (*AgentManifest, error) {
	agentDef, err := ExtractAgentDefinition(manifestYamlContent)
	if err != nil {
		return nil, fmt.Errorf("YAML content does not conform to AgentManifest format: %w", err)
	}

	var manifest AgentManifest
	if err := yaml.Unmarshal(manifestYamlContent, &manifest); err != nil {
		return nil, fmt.Errorf("YAML content does not conform to AgentManifest format: %w", err)
	}
	manifest.Template = agentDef

	if err := ValidateAgentManifest(&manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// Returns a specific agent definition based on the "kind" field in the template
func ExtractAgentDefinition(manifestYamlContent []byte) (any, error) {
	var genericManifest map[string]interface{}
	if err := yaml.Unmarshal(manifestYamlContent, &genericManifest); err != nil {
		return nil, fmt.Errorf("YAML content is not valid: %w", err)
	}

	template := genericManifest["template"].(map[string]interface{})
	templateBytes, _ := yaml.Marshal(template)

	var agentDef AgentDefinition
	if err := yaml.Unmarshal(templateBytes, &agentDef); err != nil {
		return nil, fmt.Errorf("failed to unmarshal to AgentDefinition: %v\n", err)
	}

	switch agentDef.Kind {
	case AgentKindPrompt:
		var agent PromptAgent
		if err := yaml.Unmarshal(templateBytes, &agent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to PromptAgent: %v\n", err)
		}

		agent.AgentDefinition = agentDef
		return agent, nil
	case AgentKindHosted:
		var agent HostedContainerAgent
		if err := yaml.Unmarshal(templateBytes, &agent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to HostedContainerAgent: %v\n", err)
		}

		agent.AgentDefinition = agentDef
		return agent, nil
	}

	return nil, fmt.Errorf("unrecognized agent kind: %s", agentDef.Kind)
}

// ValidateAgentManifest performs basic validation of an AgentManifest
// Returns an error if the manifest is invalid, nil if valid
func ValidateAgentManifest(manifest *AgentManifest) error {
	var errors []string

	// First, extract the kind from the template to determine the agent type
	templateBytes, _ := yaml.Marshal(manifest.Template)

	var agentDef AgentDefinition
	if err := yaml.Unmarshal(templateBytes, &agentDef); err != nil {
		errors = append(errors, "failed to parse template to determine agent kind")
	} else {
		// Validate the kind is supported
		if !IsValidAgentKind(agentDef.Kind) {
			validKinds := ValidAgentKinds()
			validKindStrings := make([]string, len(validKinds))
			for i, validKind := range validKinds {
				validKindStrings[i] = string(validKind)
			}
			errors = append(errors, fmt.Sprintf("template.kind must be one of: %v, got '%s'", validKindStrings, agentDef.Kind))
		} else {
			switch AgentKind(agentDef.Kind) {
			case AgentKindPrompt:
				var agent PromptAgent
				if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
					if agent.Name == "" {
						errors = append(errors, "template.name is required")
					}
					if agent.Model.Id == "" {
						errors = append(errors, "template.model.id is required")
					}
				} else {
					errors = append(errors, fmt.Sprintf("Failed to unmarshal to PromptAgent: %v\n", err))
				}
			case AgentKindHosted:
				var agent HostedContainerAgent
				if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
					if agent.Name == "" {
						errors = append(errors, "template.name is required")
					}
					// TODO: Do we need this?
					// if len(agent.Models) == 0 {
					// 	errors = append(errors, "template.models is required and must not be empty")
					// }
				} else {
					errors = append(errors, fmt.Sprintf("Failed to unmarshal to HostedContainerAgent: %v\n", err))
				}
			case AgentKindWorkflow:
				var agent WorkflowAgent
				if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
					if agent.Name == "" {
						errors = append(errors, "template.name is required")
					}
					// WorkflowAgent doesn't have models, so no model validation needed
				} else {
					errors = append(errors, fmt.Sprintf("Failed to unmarshal to WorkflowAgent: %v\n", err))
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
