// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

func addAgentToolboxReference(path string, reference agent_yaml.ToolboxReference) error {
	reference.Name = strings.TrimSpace(reference.Name)
	reference.Version = strings.TrimSpace(reference.Version)
	if reference.Name == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidToolbox,
			"toolbox name must not be empty",
			"pass a toolbox name",
		)
	}

	// #nosec G304 -- reading the user-selected agent definition is intentional.
	data, err := os.ReadFile(path)
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeAgentDefinitionNotFound,
			fmt.Sprintf("failed to read agent definition %q: %s", path, err),
			"run the command from the agent directory or pass --file <path>",
		)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent definition %q is invalid YAML: %s", path, err),
			"fix the agent definition and retry",
		)
	}
	root, err := agentDefinitionMapping(&document)
	if err != nil {
		return err
	}
	if existing := mappingNodeValue(root, "toolbox"); existing != nil {
		var current agent_yaml.ToolboxReference
		if err := existing.Decode(&current); err != nil {
			return exterrors.Validation(
				exterrors.CodeInvalidToolbox,
				fmt.Sprintf("existing toolbox reference in %q is invalid: %s", path, err),
				"fix or remove the existing toolbox reference and retry",
			)
		}
		if current == reference {
			return nil
		}
		return exterrors.Validation(
			exterrors.CodeInvalidToolbox,
			fmt.Sprintf("agent definition %q already references toolbox %q", path, current.Name),
			"remove or update the existing toolbox reference before adding another",
		)
	}

	toolboxNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	toolboxNode.Content = append(toolboxNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: reference.Name},
	)
	if reference.Version != "" {
		toolboxNode.Content = append(toolboxNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "version"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: reference.Version},
		)
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "toolbox"},
		toolboxNode,
	)
	content, err := yaml.Marshal(&document)
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to encode agent definition %q: %s", path, err),
		)
	}
	if err := azdext.WriteFileAtomic(path, content, 0); err != nil {
		return exterrors.Dependency(
			exterrors.CodeInvalidFilePath,
			fmt.Sprintf("failed to save agent definition %q: %s", path, err),
			"verify the file and directory permissions, then retry",
		)
	}
	return nil
}

func agentDefinitionMapping(document *yaml.Node) (*yaml.Node, error) {
	if document == nil || document.Kind != yaml.DocumentNode || len(document.Content) != 1 ||
		document.Content[0].Kind != yaml.MappingNode {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			"agent definition must be a YAML mapping",
			"fix the agent.yaml structure and retry",
		)
	}
	return document.Content[0], nil
}

func mappingNodeValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}
