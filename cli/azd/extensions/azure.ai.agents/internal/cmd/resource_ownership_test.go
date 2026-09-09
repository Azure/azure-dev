// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"os"
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestAgentDependencyCommandsRemainRegistered(t *testing.T) {
	// The SDK changes Cobra's global traversal setting when constructing a root.
	root := NewRootCommand()
	for _, kind := range []string{"toolbox", "connection"} {
		command, remaining, err := root.Find([]string{kind, "add"})
		require.NoError(t, err)
		require.Empty(t, remaining)
		require.Equal(t, "add", command.Name())
		require.Equal(t, kind, command.Parent().Name())
		require.Equal(t, "agent "+kind+" add", command.CommandPath())
		require.NotNil(t, command.RunE)
		require.NotNil(t, command.Flags().Lookup("agent"))
		require.NotNil(t, command.InheritedFlags().Lookup("output"))
		require.NoError(t, command.Args(command, []string{"dependency"}))
		require.Error(t, command.Args(command, nil))
	}
}

func TestAgentRootRejectsOldAddCommandOrder(t *testing.T) {
	for _, kind := range []string{"toolbox", "connection"} {
		t.Run(kind, func(t *testing.T) {
			root := NewRootCommand()
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs([]string{"add", kind, "dependency", "--agent", "research-agent"})
			require.ErrorContains(t, root.ExecuteContext(t.Context()), `unknown command "add"`)
		})
	}
}

func TestAgentRootDoesNotExposeStandaloneDeploy(t *testing.T) {
	// Root construction changes Cobra's global traversal setting.
	root := NewRootCommand()
	for _, command := range root.Commands() {
		assert.NotEqual(t, "deploy", command.Name())
		assert.NotContains(t, command.Aliases, "deploy")
	}
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"deploy", "./agent.yaml"})
	require.ErrorContains(t, root.ExecuteContext(t.Context()), `unknown command "deploy"`)
}

func TestAgentCoreDeployServiceTargetRemainsRegistered(t *testing.T) {
	t.Parallel()
	// Registration and initialization must be local; no daemon or Azure calls.
	host := azdext.NewExtensionHost(nil)
	configureExtensionHost(host)
	targets := host.ServiceTargets()
	require.Len(t, targets, 1)
	require.Equal(t, AiAgentHost, targets[0].Host)
	require.NotNil(t, targets[0].Factory)
	provider := targets[0].Factory()
	require.IsType(t, &project.AgentServiceTargetProvider{}, provider)
	require.NoError(t, provider.Initialize(t.Context(), &azdext.ServiceConfig{
		Name: "research-agent", Host: AiAgentHost,
	}))
}

type resourceExtensionManifest struct {
	Dependencies []struct {
		ID      string `yaml:"id"`
		Version string `yaml:"version"`
	} `yaml:"dependencies"`
	Providers []struct {
		Name string `yaml:"name"`
		Type string `yaml:"type"`
	} `yaml:"providers"`
}

func TestFoundryResourceOwnershipMetadata(t *testing.T) {
	t.Parallel()

	agents := readResourceExtensionManifest(t, "../../extension.yaml")
	connections := readResourceExtensionManifest(t, "../../../azure.ai.connections/extension.yaml")
	toolboxes := readResourceExtensionManifest(t, "../../../azure.ai.toolboxes/extension.yaml")

	assert.True(t, hasResourceDependency(agents, "azure.ai.connections", "~1.0.0-beta.6"))
	assert.True(t, hasResourceDependency(agents, "azure.ai.toolboxes", "~1.0.0-beta.6"))
	assert.True(t, hasResourceProvider(agents, "azure.ai.agent", "service-target"))
	assert.False(t, hasResourceProvider(agents, "azure.ai.connection", "service-target"))
	assert.False(t, hasResourceProvider(agents, "azure.ai.toolbox", "service-target"))
	assert.True(t, hasResourceProvider(connections, "azure.ai.connection", "service-target"))
	assert.True(t, hasResourceProvider(toolboxes, "azure.ai.toolbox", "service-target"))
	assert.True(t, hasResourceDependency(toolboxes, "azure.ai.connections", "~1.0.0-beta.6"))
}

func readResourceExtensionManifest(t *testing.T, path string) resourceExtensionManifest {
	t.Helper()
	//nolint:gosec // repository-controlled manifest path
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var manifest resourceExtensionManifest
	require.NoError(t, yaml.Unmarshal(data, &manifest))
	return manifest
}

func hasResourceDependency(manifest resourceExtensionManifest, id, version string) bool {
	for _, dependency := range manifest.Dependencies {
		if dependency.ID == id && dependency.Version == version {
			return true
		}
	}
	return false
}

func hasResourceProvider(manifest resourceExtensionManifest, name, providerType string) bool {
	for _, provider := range manifest.Providers {
		if provider.Name == name && provider.Type == providerType {
			return true
		}
	}
	return false
}
