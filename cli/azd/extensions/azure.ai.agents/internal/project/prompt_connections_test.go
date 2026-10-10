// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestConnectionsNodeRequiresSiblingMarker(t *testing.T) {
	agent := &agent_yaml.PromptAgent{Connections: []string{"search"}}
	graph := &promptGraph{
		managed: agent,
		projectServices: map[string]*azdext.ServiceConfig{
			"search": promptConnectionService(t, "search", ""),
		},
		env: map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": "https://acct.services.ai.azure.com/api/projects/project",
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

	graph.env[envkey.ConnectionServiceProjectEndpoint("search")] = graph.projectEndpoint()
	if err := node.Resolve(t.Context()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestConnectionsNodeRejectsCrossProjectMarker(t *testing.T) {
	agent := &agent_yaml.PromptAgent{Connections: []string{"search"}}
	graph := &promptGraph{
		managed: agent,
		projectServices: map[string]*azdext.ServiceConfig{
			"search": promptConnectionService(t, "search", ""),
		},
		env: map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":                        "https://acct.services.ai.azure.com/api/projects/current",
			envkey.ConnectionServiceProjectEndpoint("search"): "https://acct.services.ai.azure.com/api/projects/other",
		},
	}

	node := connectionsNode(graph)
	require.NoError(t, node.Validate())
	if err := node.Resolve(t.Context()); err == nil {
		t.Fatal("expected cross-project marker error")
	}
}

func TestConnectionsNodeUsesResolvedPromptProject(t *testing.T) {
	agent := &agent_yaml.PromptAgent{Connections: []string{"search"}}
	projectEndpoint := "https://acct.services.ai.azure.com/api/projects/explicit"
	graph := &promptGraph{
		managed:  agent,
		settings: &PromptAgentSettings{ProjectEndpoint: projectEndpoint},
		projectServices: map[string]*azdext.ServiceConfig{
			"search": promptConnectionService(t, "search", ""),
		},
		env: map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":                        "https://acct.services.ai.azure.com/api/projects/environment",
			envkey.ConnectionServiceProjectEndpoint("search"): projectEndpoint,
		},
	}

	node := connectionsNode(graph)
	require.NoError(t, node.Validate())
	if err := node.Resolve(t.Context()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestConnectionsNodeResolvesConfiguredConnectionName(t *testing.T) {
	for _, reference := range []string{"search-service", "search-resource"} {
		t.Run(reference, func(t *testing.T) {
			agent := &agent_yaml.PromptAgent{Connections: []string{reference}}
			graph := &promptGraph{
				managed: agent,
				projectServices: map[string]*azdext.ServiceConfig{
					"search-service": promptConnectionService(t, "search-service", "search-resource"),
				},
				env: map[string]string{
					"FOUNDRY_PROJECT_ENDPOINT": "https://acct.services.ai.azure.com/api/projects/project",
				},
			}

			graph.env[envkey.ConnectionServiceProjectEndpoint("search-service")] = graph.projectEndpoint()
			node := connectionsNode(graph)
			require.NoError(t, node.Validate())
			require.NoError(t, node.Resolve(t.Context()))
		})
	}
}

func TestConnectionsNodeUsesResolvedRefNameAndServiceKeyMarker(t *testing.T) {
	t.Parallel()
	for _, file := range []struct {
		name    string
		content string
	}{
		{name: "connection.yaml", content: "name: referenced-name\n"},
		{name: "connection.json", content: `{"name":"referenced-name"}`},
	} {
		t.Run(file.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, file.name), []byte(file.content), 0o600))

			const serviceKey = "search-service"
			const endpoint = "https://acct.services.ai.azure.com/api/projects/project"
			graph := &promptGraph{
				managed:     &agent_yaml.PromptAgent{Connections: []string{"overlay-name"}},
				projectRoot: root,
				projectServices: map[string]*azdext.ServiceConfig{
					serviceKey: {
						Name: serviceKey,
						Host: foundryConnectionHost,
						AdditionalProperties: mustStruct(t, map[string]any{
							"$ref": "./" + file.name,
							"name": "overlay-name",
						}),
					},
				},
				env: map[string]string{
					"FOUNDRY_PROJECT_ENDPOINT":                          endpoint,
					envkey.ConnectionServiceProjectEndpoint(serviceKey): endpoint,
				},
			}

			node := connectionsNode(graph)
			require.NoError(t, node.Validate())
			require.NoError(t, node.Resolve(t.Context()))
		})
	}
}

func TestConnectionsNodeDeduplicatesAliasesToSameService(t *testing.T) {
	t.Parallel()
	const serviceKey = "search-service"
	const endpoint = "https://acct.services.ai.azure.com/api/projects/project"
	graph := &promptGraph{
		managed: &agent_yaml.PromptAgent{Connections: []string{
			"search-service", "search-resource", "search-resource",
		}},
		projectServices: map[string]*azdext.ServiceConfig{
			serviceKey: promptConnectionService(t, serviceKey, "search-resource"),
		},
		env: map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":                          endpoint,
			envkey.ConnectionServiceProjectEndpoint(serviceKey): endpoint,
		},
	}

	node := connectionsNode(graph)
	require.NoError(t, node.Validate())
	require.NoError(t, node.Resolve(t.Context()))
}

func TestConnectionsNodeRequiresLocalConnectionService(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		services map[string]*azdext.ServiceConfig
		want     string
	}{
		{name: "missing", want: "does not match an azure.ai.connection service"},
		{
			name: "wrong host",
			services: map[string]*azdext.ServiceConfig{
				"search": {Name: "search", Host: foundryToolboxHost},
			},
			want: "instead of \"azure.ai.connection\"",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			node := connectionsNode(&promptGraph{
				managed:         &agent_yaml.PromptAgent{Connections: []string{"search"}},
				projectServices: test.services,
			})
			require.ErrorContains(t, node.Validate(), test.want)
		})
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

func TestConnectionsNodeRejectsLegacyAndOtherServiceMarkers(t *testing.T) {
	t.Parallel()
	const endpoint = "https://acct.services.ai.azure.com/api/projects/project"
	for _, tt := range []struct {
		name string
		env  map[string]string
	}{
		{
			name: "aggregate markers",
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":                      endpoint,
				"AZURE_AI_PROJECT_CONNECTION_NAMES":             "search-resource",
				"AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT": endpoint,
			},
		},
		{
			name: "resource name instead of service key",
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":                                 endpoint,
				envkey.ConnectionServiceProjectEndpoint("search-resource"): endpoint,
			},
		},
		{
			name: "case-colliding service key",
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":                                endpoint,
				envkey.ConnectionServiceProjectEndpoint("Search-service"): endpoint,
			},
		},
		{
			name: "service marker without active project",
			env: map[string]string{
				envkey.ConnectionServiceProjectEndpoint("search-service"): endpoint,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			graph := &promptGraph{
				managed: &agent_yaml.PromptAgent{Connections: []string{"search-service"}},
				projectServices: map[string]*azdext.ServiceConfig{
					"search-service": promptConnectionService(t, "search-service", "search-resource"),
				},
				env: tt.env,
			}
			node := connectionsNode(graph)
			require.NoError(t, node.Validate())
			require.ErrorContains(t, node.Resolve(t.Context()), `connection "search-resource" has not been deployed`)
		})
	}
}

func promptConnectionService(t *testing.T, serviceKey, resourceName string) *azdext.ServiceConfig {
	t.Helper()
	service := &azdext.ServiceConfig{Name: serviceKey, Host: foundryConnectionHost}
	if resourceName != "" {
		service.AdditionalProperties = mustStruct(t, map[string]any{"name": resourceName})
	}
	return service
}
