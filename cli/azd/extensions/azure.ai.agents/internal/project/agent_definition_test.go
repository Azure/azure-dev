// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// sampleContainerAgent returns a hosted ContainerAgent with the fields that the
// unified inline shape must round-trip.
func sampleContainerAgent() agent_yaml.ContainerAgent {
	return agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind:        agent_yaml.AgentKindHosted,
			Name:        "basic-agent",
			Description: new("A basic agent hosted by Foundry."),
		},
		Protocols: []agent_yaml.ProtocolVersionRecord{
			{Protocol: "responses", Version: "1.0.0"},
		},
		EnvironmentVariables: &[]agent_yaml.EnvironmentVariable{
			{Name: "FOUNDRY_MODEL_DEPLOYMENT_NAME", Value: "gpt-4.1-mini"},
		},
		Resources: &agent_yaml.ContainerResources{Cpu: "1", Memory: "2Gi"},
	}
}

// TestAgentDefinitionRoundTrip verifies that a hosted agent definition plus the
// deploy/provision config survive a marshal into the inline service properties
// and back, including the key/type collision between the CPU/memory `resources`
// (container) and the tool `resources` list.
func TestAgentDefinitionRoundTrip(t *testing.T) {
	ca := sampleContainerAgent()
	extra := &ServiceTargetAgentConfig{
		StartupCommand: "python main.py",
		Resources: []Resource{
			{Resource: "bing_grounding", ConnectionName: "bing"},
		},
		ToolConnections: []ToolConnection{
			{Name: "mcp", Category: "RemoteTool", Target: "https://example", AuthType: "None"},
		},
	}

	props, err := AgentDefinitionToServiceProperties(ca, extra)
	require.NoError(t, err)

	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}

	got, isHosted, found, source, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, isHosted)
	require.Equal(t, AgentDefinitionSourceInline, source)
	require.False(t, source.IsLegacy())

	require.Equal(t, "basic-agent", got.Name)
	require.NotNil(t, got.Description)
	require.Equal(t, "A basic agent hosted by Foundry.", *got.Description)
	require.Equal(t, ca.Protocols, got.Protocols)
	require.NotNil(t, got.EnvironmentVariables)
	require.Equal(t, *ca.EnvironmentVariables, *got.EnvironmentVariables)
	// CPU/memory round-trips through the `container` config.
	require.NotNil(t, got.Resources)
	require.Equal(t, "1", got.Resources.Cpu)
	require.Equal(t, "2Gi", got.Resources.Memory)

	// The deploy/provision config survives alongside the definition. The tool
	// `resources` list must NOT be clobbered by the CPU/memory `resources`.
	cfg, err := LoadServiceTargetAgentConfig(svc)
	require.NoError(t, err)
	require.Equal(t, "python main.py", cfg.StartupCommand)
	require.Len(t, cfg.Resources, 1)
	require.Equal(t, "bing_grounding", cfg.Resources[0].Resource)
	require.Len(t, cfg.ToolConnections, 1)
	require.NotNil(t, cfg.Container)
	require.NotNil(t, cfg.Container.Resources)
	require.Equal(t, "1", cfg.Container.Resources.Cpu)
}

// TestAgentDefinitionFromService_LegacyConfigShape verifies that a definition
// stored under the deprecated config-nested shape is detected as legacy.
func TestAgentDefinitionFromService_LegacyConfigShape(t *testing.T) {
	props, err := AgentDefinitionToServiceProperties(sampleContainerAgent(), nil)
	require.NoError(t, err)

	svc := &azdext.ServiceConfig{
		Name:   "basic-agent",
		Host:   "azure.ai.agent",
		Config: props, // old config-nested shape
	}

	got, isHosted, found, source, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, isHosted)
	require.Equal(t, AgentDefinitionSourceLegacyConfig, source)
	require.True(t, source.IsLegacy())
	require.Equal(t, "basic-agent", got.Name)
}

// TestAgentDefinitionFromService_NoDefinition verifies that a service without an
// inline definition reports not-found (callers then fall back to disk).
func TestAgentDefinitionFromService_NoDefinition(t *testing.T) {
	svc := &azdext.ServiceConfig{Name: "basic-agent", Host: "azure.ai.agent"}
	_, _, found, _, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	require.False(t, found)
}

// TestLoadAgentDefinition_DiskFallback verifies the legacy on-disk agent.yaml
// fallback used during the migration window.
func TestLoadAgentDefinition_DiskFallback(t *testing.T) {
	dir := t.TempDir()
	yaml := "kind: hosted\nname: disk-agent\nprotocols:\n  - protocol: responses\n    version: \"1.0.0\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0o600))

	svc := &azdext.ServiceConfig{Name: "disk-agent", Host: "azure.ai.agent", RelativePath: "."}
	got, isHosted, source, err := LoadAgentDefinition(svc, dir)
	require.NoError(t, err)
	require.True(t, isHosted)
	require.Equal(t, AgentDefinitionSourceDisk, source)
	require.True(t, source.IsLegacy())
	require.Equal(t, "disk-agent", got.Name)
}

// TestUpsertAgentEnvVars verifies that env vars are added/updated on the inline
// definition while preserving the other definition keys.
func TestUpsertAgentEnvVars(t *testing.T) {
	props, err := AgentDefinitionToServiceProperties(sampleContainerAgent(), nil)
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{Name: "basic-agent", Host: "azure.ai.agent", AdditionalProperties: props}

	require.NoError(t, UpsertAgentEnvVars(svc, map[string]string{
		"FOUNDRY_MODEL_DEPLOYMENT_NAME": "gpt-4o", // update existing
		"OPTIMIZATION_CANDIDATE_ID":     "cand-1", // add new
	}))

	got, _, found, _, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "basic-agent", got.Name) // other keys preserved
	require.NotNil(t, got.EnvironmentVariables)

	values := map[string]string{}
	for _, ev := range *got.EnvironmentVariables {
		values[ev.Name] = ev.Value
	}
	require.Equal(t, "gpt-4o", values["FOUNDRY_MODEL_DEPLOYMENT_NAME"])
	require.Equal(t, "cand-1", values["OPTIMIZATION_CANDIDATE_ID"])
}
