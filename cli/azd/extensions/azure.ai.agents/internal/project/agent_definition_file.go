// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/braydonk/yaml"
)

var agentDefinitionFileNames = []string{"agent.yaml", "agent.yml", "agent.manifest.yaml", "agent.manifest.yml"}

func agentDefinitionOverridePath() (string, error) {
	path := os.Getenv("AGENT_DEFINITION_PATH")
	if path == "" {
		return "", nil
	}
	// #nosec G703 -- AGENT_DEFINITION_PATH is an explicit user override, allowed outside the project.
	info, err := os.Stat(path)
	if err != nil {
		return "", exterrors.Validation(exterrors.CodeAgentDefinitionNotFound,
			fmt.Sprintf("cannot read agent definition specified in AGENT_DEFINITION_PATH: %s",
				redactPreviewString(err.Error())),
			"set AGENT_DEFINITION_PATH to a readable YAML file")
	}
	if !info.Mode().IsRegular() {
		return "", exterrors.Validation(exterrors.CodeAgentDefinitionNotFound,
			fmt.Sprintf("AGENT_DEFINITION_PATH must identify a file: %s", redactPreviewString(path)),
			"set AGENT_DEFINITION_PATH to a YAML file, not a directory")
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".yaml" && ext != ".yml" {
		return "", exterrors.Validation(exterrors.CodeAgentDefinitionNotFound,
			fmt.Sprintf("agent definition file must be a YAML file (.yaml or .yml), got: %s", ext),
			"provide a file with .yaml or .yml extension")
	}
	return path, nil
}

func findAgentDefinitionFile(service *azdext.ServiceConfig, projectRoot string) (string, error) {
	for _, name := range agentDefinitionFileNames {
		path, err := paths.JoinAllowRoot(projectRoot, service.GetRelativePath(), name)
		if err != nil {
			return "", exterrors.Validation(exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("invalid service path for %s: %s", service.GetName(), err),
				"update azure.yaml so the agent definition stays within the project directory")
		}
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect agent definition %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return "", exterrors.Validation(exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("agent definition %q must be a file", path),
				"provide a readable YAML file in the service directory")
		}
		return path, nil
	}
	return "", exterrors.Dependency(exterrors.CodeAgentDefinitionNotFound,
		fmt.Sprintf("agent definition not found for service %q", service.GetName()),
		"define the agent in azure.yaml, add agent.yaml/agent.manifest.yaml to the service directory, "+
			"or set AGENT_DEFINITION_PATH")
}

func loadAgentDefinitionFile(path string, includeCompanions bool) (agent_yaml.ContainerAgent, bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // The path is a validated service file or an explicit user override.
	if err != nil {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to read agent definition: %s", redactPreviewString(err.Error())),
			"verify the agent definition file exists and is readable")
	}
	if !includeCompanions {
		return parseContainerAgentYAML(data)
	}
	var header agent_yaml.AgentDefinition
	if err := yaml.Unmarshal(data, &header); err != nil {
		return parseContainerAgentYAML(data)
	}
	if header.Kind != "" && header.Kind != agent_yaml.AgentKindHosted {
		return parseContainerAgentYAML(data)
	}
	loaded, err := LoadAgentPreviewDefinition(path, nil)
	if err != nil {
		return agent_yaml.ContainerAgent{}, false, err
	}
	return loaded.Definition, true, nil
}

func agentSourceDirectory(serviceDirectory, definitionPath string, explicitOverride bool) string {
	if definitionPath != "" && (explicitOverride || serviceDirectory == "") {
		return filepath.Dir(definitionPath)
	}
	return serviceDirectory
}
