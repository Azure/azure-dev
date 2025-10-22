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

	// Validate Agent Definition - only the essential fields
	if manifest.Agent.Name == "" {
		errors = append(errors, "agent.name is required")
	}
	if manifest.Agent.Kind == "" {
		errors = append(errors, "agent.kind is required")
	} else if !IsValidAgentKind(manifest.Agent.Kind) {
		validKinds := ValidAgentKinds()
		validKindStrings := make([]string, len(validKinds))
		for i, kind := range validKinds {
			validKindStrings[i] = string(kind)
		}
		errors = append(errors, fmt.Sprintf("agent.kind must be one of: %v, got '%s'", validKindStrings, manifest.Agent.Kind))
	}
	if manifest.Agent.Model.Id == "" {
		errors = append(errors, "agent.model.id is required")
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
