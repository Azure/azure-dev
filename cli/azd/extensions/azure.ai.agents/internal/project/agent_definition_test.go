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
	"google.golang.org/protobuf/types/known/structpb"
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
			{Protocol: "responses", Version: "2.0.0"},
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

func TestSetAgentContainerSettings_ReturnsPersistenceTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		legacy   bool
		wantPath string
	}{
		{
			name:     "inline service properties",
			wantPath: "container",
		},
		{
			name:     "legacy config properties",
			legacy:   true,
			wantPath: "config.container",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			props, err := structpb.NewStruct(map[string]any{"customField": "preserved"})
			require.NoError(t, err)

			svc := &azdext.ServiceConfig{}
			if tt.legacy {
				svc.Config = props
			} else {
				svc.AdditionalProperties = props
			}

			path, value, err := SetAgentContainerSettings(svc, &ContainerSettings{
				Resources: &ResourceSettings{Cpu: "1", Memory: "2Gi"},
			})
			require.NoError(t, err)
			require.Equal(t, tt.wantPath, path)
			require.Equal(t, map[string]any{
				"resources": map[string]any{
					"cpu":    "1",
					"memory": "2Gi",
				},
			}, value.AsInterface())

			storedProps := ServiceConfigProps(svc)
			require.Equal(t, "preserved", storedProps.GetFields()["customField"].GetStringValue())
			require.Same(t, value, storedProps.GetFields()["container"])
		})
	}
}

// TestAgentDefinition_ImageRidesOnCoreServiceField verifies the prebuilt image
// maps onto the core ServiceConfig.Image field (which core binds and round-trips)
// rather than the inline property bag, where core would strip it on reload.
func TestAgentDefinition_ImageRidesOnCoreServiceField(t *testing.T) {
	const image = "myregistry.azurecr.io/img:v1"
	ca := sampleContainerAgent()
	ca.Image = image

	props, err := AgentDefinitionToServiceProperties(ca, nil)
	require.NoError(t, err)
	// image must NOT be carried in the inline AdditionalProperties: core binds
	// the typed `image` field, so an inline `image` key is dropped on reload.
	_, hasInlineImage := props.GetFields()["image"]
	require.False(t, hasInlineImage, "image must not be carried in inline AdditionalProperties")

	// The definition reads its image back from the core service field.
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 "azure.ai.agent",
		Image:                image,
		AdditionalProperties: props,
	}
	got, isHosted, found, _, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, isHosted)
	require.Equal(t, image, got.Image)

	// With no core image field, image is empty — proving it is not in props.
	svcNoImage := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}
	gotNoImage, _, _, _, err := AgentDefinitionFromService(svcNoImage)
	require.NoError(t, err)
	require.Empty(t, gotNoImage.Image)
}

// TestAgentDefinitionFromService_InvalidImage verifies the image reference (from
// the core service field) is still validated for the inline shape.
func TestAgentDefinitionFromService_InvalidImage(t *testing.T) {
	props, err := AgentDefinitionToServiceProperties(sampleContainerAgent(), nil)
	require.NoError(t, err)

	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 "azure.ai.agent",
		Image:                "not a valid image ref",
		AdditionalProperties: props,
	}
	_, _, _, _, err = AgentDefinitionFromService(svc)
	require.Error(t, err)
}

// TestAgentDefinitionFromService_InvalidDefinition verifies that inline
// definitions get the same structural validation as the on-disk agent.yaml path,
// so a malformed definition (e.g. an invalid agent name) is not silently used.
func TestAgentDefinitionFromService_InvalidDefinition(t *testing.T) {
	ca := sampleContainerAgent()
	ca.Name = "Invalid Name!" // fails ValidateAgentName
	props, err := AgentDefinitionToServiceProperties(ca, nil)
	require.NoError(t, err)

	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}
	_, _, _, _, err = AgentDefinitionFromService(svc)
	require.Error(t, err)
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
