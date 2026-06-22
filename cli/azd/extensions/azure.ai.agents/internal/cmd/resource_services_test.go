// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustMarshalConfig[T any](t *testing.T, in *T) *azdext.ServiceConfig {
	t.Helper()
	cfg, err := project.MarshalStruct(in)
	require.NoError(t, err)
	return &azdext.ServiceConfig{Config: cfg}
}

func projectService(t *testing.T, name string, deployments ...project.Deployment) *azdext.ServiceConfig {
	t.Helper()
	svc := mustMarshalConfig(t, &project.ServiceTargetAgentConfig{Deployments: deployments})
	svc.Name = name
	svc.Host = AiProjectHost
	return svc
}

func connectionService(t *testing.T, name string, conn project.Connection) *azdext.ServiceConfig {
	t.Helper()
	svc := mustMarshalConfig(t, &conn)
	svc.Name = name
	svc.Host = AiConnectionHost
	return svc
}

func toolboxService(t *testing.T, name string, toolbox project.Toolbox) *azdext.ServiceConfig {
	t.Helper()
	svc := mustMarshalConfig(t, &toolbox)
	svc.Name = name
	svc.Host = AiToolboxHost
	return svc
}

func agentService(t *testing.T, name string, toolConnections ...project.ToolConnection) *azdext.ServiceConfig {
	t.Helper()
	svc := mustMarshalConfig(t, &project.ServiceTargetAgentConfig{ToolConnections: toolConnections})
	svc.Name = name
	svc.Host = AiAgentHost
	return svc
}

// TestSanitizeServiceName verifies resource names are normalized into valid
// azure.yaml service keys (spaces removed, surrounding whitespace trimmed).
func TestSanitizeServiceName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "MyAgent", sanitizeServiceName("  My Agent  "))
	assert.Equal(t, "gpt4o", sanitizeServiceName("gpt 4 o"))
	assert.Equal(t, "", sanitizeServiceName("   "))
}

// TestCollectProjectDeployments verifies deployments are sourced only from
// azure.ai.project services and ignore sibling hosts.
func TestCollectProjectDeployments(t *testing.T) {
	t.Parallel()

	dep := project.Deployment{Name: "gpt-4o", Model: project.DeploymentModel{Name: "gpt-4o"}}
	services := map[string]*azdext.ServiceConfig{
		"ai-project": projectService(t, "ai-project", dep),
		"agent":      agentService(t, "agent"),
		"conn":       connectionService(t, "conn", project.Connection{Name: "conn"}),
	}

	deployments, err := collectProjectDeployments(services)
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	assert.Equal(t, "gpt-4o", deployments[0].Name)
}

// TestCollectConnections verifies connections are sourced from
// azure.ai.connection services in deterministic (sorted) order.
func TestCollectConnections(t *testing.T) {
	t.Parallel()

	services := map[string]*azdext.ServiceConfig{
		"zeta":       connectionService(t, "zeta", project.Connection{Name: "zeta", Category: "ApiKey"}),
		"alpha":      connectionService(t, "alpha", project.Connection{Name: "alpha", Category: "ApiKey"}),
		"ai-project": projectService(t, "ai-project"),
		"agent":      agentService(t, "agent"),
	}

	connections, err := collectConnections(services)
	require.NoError(t, err)
	require.Len(t, connections, 2)
	// Sorted by service key (alpha before zeta) for stable env-var output.
	assert.Equal(t, "alpha", connections[0].Name)
	assert.Equal(t, "zeta", connections[1].Name)
}

// TestCollectToolboxes verifies toolboxes are sourced from azure.ai.toolbox
// services only.
func TestCollectToolboxes(t *testing.T) {
	t.Parallel()

	services := map[string]*azdext.ServiceConfig{
		"tb":    toolboxService(t, "tb", project.Toolbox{Name: "tb", Tools: []map[string]any{{"type": "mcp"}}}),
		"agent": agentService(t, "agent"),
	}

	toolboxes, err := collectToolboxes(services)
	require.NoError(t, err)
	require.Len(t, toolboxes, 1)
	assert.Equal(t, "tb", toolboxes[0].Name)
	require.Len(t, toolboxes[0].Tools, 1)
}

// TestCollectAgentToolConnections verifies tool connections stay on the agent
// service and are sourced from there for toolbox enrichment.
func TestCollectAgentToolConnections(t *testing.T) {
	t.Parallel()

	tc := project.ToolConnection{Name: "mcp-conn", Category: "CustomKeys", Target: "https://example.com"}
	services := map[string]*azdext.ServiceConfig{
		"agent":      agentService(t, "agent", tc),
		"ai-project": projectService(t, "ai-project"),
	}

	toolConnections, err := collectAgentToolConnections(services)
	require.NoError(t, err)
	require.Len(t, toolConnections, 1)
	assert.Equal(t, "mcp-conn", toolConnections[0].Name)
}

// TestCollectHelpers_EmptyAndNilConfigs verifies the collectors tolerate
// services with nil config and unrelated hosts without error.
func TestCollectHelpers_EmptyAndNilConfigs(t *testing.T) {
	t.Parallel()

	services := map[string]*azdext.ServiceConfig{
		"web":    {Name: "web", Host: "containerapp"},
		"nilcfg": {Name: "nilcfg", Host: AiProjectHost},
	}

	deployments, err := collectProjectDeployments(services)
	require.NoError(t, err)
	assert.Empty(t, deployments)

	connections, err := collectConnections(services)
	require.NoError(t, err)
	assert.Empty(t, connections)

	toolboxes, err := collectToolboxes(services)
	require.NoError(t, err)
	assert.Empty(t, toolboxes)
}
