// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"go.yaml.in/yaml/v3"
)

func loadAgentToolboxReference(path string) (*agent_yaml.ToolboxReference, error) {
	// #nosec G304 -- reading the user-selected agent definition is intentional.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeAgentDefinitionNotFound,
			fmt.Sprintf("failed to read agent definition %q: %s", path, err),
			"run the command from the agent directory or pass --file <path>",
		)
	}
	var value struct {
		Toolbox *agent_yaml.ToolboxReference `yaml:"toolbox"`
	}
	if err := yaml.Unmarshal(data, &value); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent definition %q is invalid YAML: %s", path, err),
			"fix the agent definition and retry",
		)
	}
	return value.Toolbox, nil
}
