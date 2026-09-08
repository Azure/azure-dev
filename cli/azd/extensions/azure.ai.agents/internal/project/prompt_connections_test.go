// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestConnectionsNodeRequiresSiblingMarker(t *testing.T) {
	agent := &agent_yaml.PromptAgent{Connections: []string{"search"}}
	graph := &promptGraph{
		managed: agent,
		env: map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":       "https://acct.services.ai.azure.com/api/projects/project",
			envkey.ConnectionProjectEndpoint: "https://acct.services.ai.azure.com/api/projects/project",
		},
	}

	node := connectionsNode(graph)
	if node == nil {
		t.Fatal("expected connection node")
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := node.Resolve(t.Context()); err == nil {
		t.Fatal("expected missing sibling marker error")
	}

	graph.env["AZURE_AI_PROJECT_CONNECTION_NAMES"] = "other,search"
	if err := node.Resolve(t.Context()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestConnectionsNodeRejectsCrossProjectMarker(t *testing.T) {
	agent := &agent_yaml.PromptAgent{Connections: []string{"search"}}
	graph := &promptGraph{
		managed: agent,
		env: map[string]string{
			"AZURE_AI_PROJECT_CONNECTION_NAMES": "search",
			"FOUNDRY_PROJECT_ENDPOINT":          "https://acct.services.ai.azure.com/api/projects/current",
			envkey.ConnectionProjectEndpoint:    "https://acct.services.ai.azure.com/api/projects/other",
		},
	}

	if err := connectionsNode(graph).Resolve(t.Context()); err == nil {
		t.Fatal("expected cross-project marker error")
	}
}

func TestConnectionsNodeUsesResolvedPromptProject(t *testing.T) {
	agent := &agent_yaml.PromptAgent{Connections: []string{"search"}}
	projectEndpoint := "https://acct.services.ai.azure.com/api/projects/explicit"
	graph := &promptGraph{
		managed:  agent,
		settings: &PromptAgentSettings{ProjectEndpoint: projectEndpoint},
		env: map[string]string{
			"AZURE_AI_PROJECT_CONNECTION_NAMES": "search",
			"FOUNDRY_PROJECT_ENDPOINT":          "https://acct.services.ai.azure.com/api/projects/environment",
			envkey.ConnectionProjectEndpoint:    projectEndpoint,
		},
	}

	if err := connectionsNode(graph).Resolve(t.Context()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestConnectionsNodeResolvesConfiguredConnectionName(t *testing.T) {
	agent := &agent_yaml.PromptAgent{Connections: []string{"search-service"}}
	graph := &promptGraph{
		managed: agent,
		projectServices: map[string]*azdext.ServiceConfig{
			"search-service": {
				Name: "search-service",
				Host: foundryConnectionHost,
				AdditionalProperties: mustStruct(t, map[string]any{
					"name": "search-resource",
				}),
			},
		},
		env: map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":          "https://acct.services.ai.azure.com/api/projects/project",
			envkey.ConnectionProjectEndpoint:    "https://acct.services.ai.azure.com/api/projects/project",
			"AZURE_AI_PROJECT_CONNECTION_NAMES": "search-resource",
		},
	}

	node := connectionsNode(graph)
	if err := node.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := node.Resolve(t.Context()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestConnectionsNodeIgnoresToolboxProjectConnectionID(t *testing.T) {
	agent := &agent_yaml.PromptAgent{Toolbox: &agent_yaml.ToolboxReference{
		Name: "tools", ProjectConnectionID: "external-connection-id",
	}}
	if node := connectionsNode(&promptGraph{managed: agent}); node != nil {
		t.Fatal("toolbox projectConnectionId should not create a sibling service dependency")
	}
}

func TestConnectionsNodeNoneReturnsNil(t *testing.T) {
	if connectionsNode(&promptGraph{managed: &agent_yaml.PromptAgent{}}) != nil {
		t.Fatal("expected nil node")
	}
}
