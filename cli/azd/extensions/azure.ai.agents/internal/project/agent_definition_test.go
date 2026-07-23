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
	_, hasInlineEnvironment := props.GetFields()["environmentVariables"]
	require.False(t, hasInlineEnvironment)

	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
		Environment:          AgentEnvironment(ca),
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

func TestAgentDefinitionFromService_LegacyEnvironment(t *testing.T) {
	props, err := AgentDefinitionToServiceProperties(
		sampleContainerAgent(),
		nil,
	)
	require.NoError(t, err)
	legacyEnvironment, err := structpb.NewValue([]any{
		map[string]any{
			"name":  "LEGACY_KEY",
			"value": "${LEGACY_KEY}",
		},
		map[string]any{
			"name":  "SHARED_KEY",
			"value": "legacy",
		},
	})
	require.NoError(t, err)
	props.Fields["environmentVariables"] = legacyEnvironment

	svc := &azdext.ServiceConfig{
		Name:   "basic-agent",
		Host:   "azure.ai.agent",
		Config: props,
		Environment: map[string]string{
			"NEW_KEY":    "new",
			"SHARED_KEY": "service",
		},
	}
	got, _, found, source, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, AgentDefinitionSourceLegacyConfig, source)
	require.Equal(t, map[string]string{
		"LEGACY_KEY": "${LEGACY_KEY}",
		"NEW_KEY":    "new",
		"SHARED_KEY": "service",
	}, AgentEnvironment(got))
}

// TestInlineAgentEnvironmentVariables verifies the raw inline
// environmentVariables are returned as authored, without merging the
// core-forwarded (already expanded) service environment.
func TestInlineAgentEnvironmentVariables(t *testing.T) {
	props, err := AgentDefinitionToServiceProperties(
		sampleContainerAgent(),
		nil,
	)
	require.NoError(t, err)
	legacyEnvironment, err := structpb.NewValue([]any{
		map[string]any{
			"name":  "LEGACY_KEY",
			"value": "${LEGACY_KEY}",
		},
		map[string]any{
			"name":  "SHARED_KEY",
			"value": "legacy",
		},
	})
	require.NoError(t, err)
	props.Fields["environmentVariables"] = legacyEnvironment

	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
		// Core-forwarded env must NOT leak into the raw result.
		Environment: map[string]string{
			"NEW_KEY":    "new",
			"SHARED_KEY": "service",
		},
	}
	got, err := InlineAgentEnvironmentVariables(svc)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"LEGACY_KEY": "${LEGACY_KEY}",
		"SHARED_KEY": "legacy",
	}, got)
}
func TestResolveAgentEnvironmentVariable(t *testing.T) {
	t.Parallel()

	t.Run("preserves same-name core value", func(t *testing.T) {
		value, err := ResolveAgentEnvironmentVariable(
			"FORWARDED_VALUE",
			"${FORWARDED_VALUE}",
			map[string]string{
				"FORWARDED_VALUE": "literal ${NOT_A_TEMPLATE}",
			},
			func(string) string {
				return "expanded"
			},
		)
		require.NoError(t, err)
		require.Equal(t, "literal ${NOT_A_TEMPLATE}", value)
	})

	t.Run("resolves aliases from service env first", func(t *testing.T) {
		value, err := ResolveAgentEnvironmentVariable(
			"TARGET",
			"${SERVICE_ENDPOINT}",
			map[string]string{
				"SERVICE_ENDPOINT": "https://service.example",
			},
			func(string) string {
				return "https://project.example"
			},
		)
		require.NoError(t, err)
		require.Equal(t, "https://service.example", value)
	})
}

func TestLoadAgentDefinition_UnrelatedInlineFallsBackToConfig(
	t *testing.T,
) {
	t.Parallel()

	config, err := AgentDefinitionToServiceProperties(
		sampleContainerAgent(),
		&ServiceTargetAgentConfig{
			StartupCommand: "python main.py",
		},
	)
	require.NoError(t, err)
	inline, err := structpb.NewStruct(map[string]any{
		"resumeSessionOnDeploy": true,
	})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: inline,
		Config:               config,
	}

	got, isHosted, source, err := LoadAgentDefinition(
		svc,
		t.TempDir(),
	)

	require.NoError(t, err)
	require.True(t, isHosted)
	require.Equal(t, AgentDefinitionSourceLegacyConfig, source)
	require.Equal(t, "basic-agent", got.Name)
	serviceConfig, err := LoadServiceTargetAgentConfig(svc)
	require.NoError(t, err)
	require.Equal(t, "python main.py", serviceConfig.StartupCommand)
}

// TestAgentDefinitionFromService_NoDefinition verifies that a service without an
// inline definition reports not-found (callers then fall back to disk).
func TestAgentDefinitionFromService_NoDefinition(t *testing.T) {
	svc := &azdext.ServiceConfig{Name: "basic-agent", Host: "azure.ai.agent"}
	_, _, found, _, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	require.False(t, found)
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

func TestLoadAgentDefinition_ResolvedKindValidation(t *testing.T) {
	tests := []struct {
		name       string
		kind       agent_yaml.AgentKind
		useFileRef bool
		wantError  bool
	}{
		{
			name:      "inline invalid kind",
			kind:      "nonsense",
			wantError: true,
		},
		{
			name: "valid workflow",
			kind: agent_yaml.AgentKindWorkflow,
		},
		{
			name:       "referenced invalid kind",
			kind:       "nonsense",
			useFileRef: true,
			wantError:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			projectRoot := t.TempDir()
			propsValues := map[string]any{
				"kind": string(test.kind),
				"name": "kind-test-agent",
			}
			if test.useFileRef {
				require.NoError(t, os.WriteFile(
					filepath.Join(projectRoot, "definition.yaml"),
					[]byte(
						"kind: "+string(test.kind)+"\n"+
							"name: kind-test-agent\n",
					),
					0o600,
				))
				propsValues = map[string]any{
					"$ref": "./definition.yaml",
				}
			}

			props, err := structpb.NewStruct(propsValues)
			require.NoError(t, err)
			svc := &azdext.ServiceConfig{
				Name:                 "kind-test-agent",
				Host:                 "azure.ai.agent",
				AdditionalProperties: props,
			}

			_, isHosted, source, err := LoadAgentDefinition(
				svc,
				projectRoot,
			)

			if test.wantError {
				require.ErrorContains(
					t,
					err,
					"template.kind must be one of",
				)
			} else {
				require.NoError(t, err)
			}
			require.False(t, isHosted)
			require.Equal(t, AgentDefinitionSourceInline, source)
		})
	}
}

func TestLoadAgentDefinition_ToolboxServiceReference(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"kind":      "hosted",
		"name":      "basic-agent",
		"toolboxes": []any{"research-tools"},
	})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}

	_, isHosted, _, err := LoadAgentDefinition(
		svc,
		t.TempDir(),
	)
	require.NoError(t, err)
	require.True(t, isHosted)

	cfg, err := LoadServiceTargetAgentConfig(svc)
	require.NoError(t, err)
	require.Len(t, cfg.Toolboxes, 1)
	require.Equal(t, "research-tools", cfg.Toolboxes[0].Name)
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

func TestLoadAgentDefinition_FileRef(t *testing.T) {
	dir := t.TempDir()
	definitionsDir := filepath.Join(dir, "definitions")
	require.NoError(t, os.MkdirAll(definitionsDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(definitionsDir, "agent.yaml"),
		[]byte(
			"kind: hosted\n"+
				"name: referenced-agent\n"+
				"startupCommand: python main.py\n"+
				"protocols:\n"+
				"  - protocol: responses\n"+
				"    version: \"1.0.0\"\n",
		),
		0o600,
	))

	props, err := structpb.NewStruct(map[string]any{
		"$ref": "./definitions/agent.yaml",
		"name": "overlay-agent",
	})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:         "agent-service",
		Host:         "azure.ai.agent",
		RelativePath: "src/agent",
		Image:        "registry.example/agent:v1",
		Docker: &azdext.DockerProjectOptions{
			Path:        "docker/Dockerfile",
			Context:     "docker",
			RemoteBuild: true,
		},
		AdditionalProperties: props,
	}

	got, isHosted, source, err := LoadAgentDefinition(svc, dir)
	require.NoError(t, err)
	require.True(t, isHosted)
	require.Equal(t, AgentDefinitionSourceInline, source)
	require.Equal(t, "overlay-agent", got.Name)
	require.Equal(t, "responses", got.Protocols[0].Protocol)
	require.Equal(t, "registry.example/agent:v1", got.Image)

	usesFileRef, err := AgentDefinitionUsesFileRef(svc, dir)
	require.NoError(t, err)
	require.True(t, usesFileRef)

	require.NoError(t, ResolveServiceConfigInPlace(svc, dir))
	_, hasRef := svc.GetAdditionalProperties().GetFields()["$ref"]
	require.False(t, hasRef)
	cfg, err := LoadServiceTargetAgentConfig(svc)
	require.NoError(t, err)
	require.Equal(t, "python main.py", cfg.StartupCommand)
	require.Equal(t, "registry.example/agent:v1", svc.GetImage())
	require.Equal(t, "docker/Dockerfile", svc.GetDocker().GetPath())
	require.Equal(t, "docker", svc.GetDocker().GetContext())
	require.True(t, svc.GetDocker().GetRemoteBuild())
}

func TestResolveServiceConfigInPlaceRejectsCoreFieldsFromRootRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{name: "env", value: "env:\n  LOG_LEVEL: info\n"},
		{name: "project", value: "project: src/agent\n"},
		{name: "language", value: "language: docker\n"},
		{name: "image", value: "image: registry.example/agent:v1\n"},
		{name: "docker", value: "docker:\n  path: Dockerfile\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(dir, "agent.yaml"),
				[]byte(tt.value),
				0o600,
			))
			props, err := structpb.NewStruct(map[string]any{
				"$ref": "./agent.yaml",
			})
			require.NoError(t, err)

			err = ResolveServiceConfigInPlace(
				&azdext.ServiceConfig{
					Name:                 "agent-service",
					AdditionalProperties: props,
				},
				dir,
			)

			require.ErrorContains(t, err, "core field \""+tt.name+"\"")
		})
	}
}

func TestAgentDefinitionUsesFileRefIgnoresNestedResourceRefs(
	t *testing.T,
) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"deployments": []any{
			map[string]any{"$ref": "./deployment.yaml"},
		},
	})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "legacy-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}

	usesFileRef, err := AgentDefinitionUsesFileRef(svc, t.TempDir())

	require.NoError(t, err)
	require.False(t, usesFileRef)
}

func TestAgentDefinitionUsesFileRefIgnoresInlineDefinition(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "settings.yaml"),
		[]byte("startupCommand: python main.py\n"),
		0o600,
	))
	props, err := structpb.NewStruct(map[string]any{
		"$ref": "./settings.yaml",
		"kind": "hosted",
		"name": "inline-agent",
	})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "inline-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}

	usesFileRef, err := AgentDefinitionUsesFileRef(svc, dir)

	require.NoError(t, err)
	require.False(t, usesFileRef)
}

// TestUpsertAgentEnvVars verifies that env vars are added/updated on the inline
// definition while preserving the other definition keys.
func TestUpsertAgentEnvVars(t *testing.T) {
	ca := sampleContainerAgent()
	props, err := AgentDefinitionToServiceProperties(ca, nil)
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
		Environment:          AgentEnvironment(ca),
	}

	require.NoError(t, UpsertAgentEnvVars(svc, map[string]string{
		"FOUNDRY_MODEL_DEPLOYMENT_NAME": "gpt-4o", // update existing
		"OPTIMIZATION_CANDIDATE_ID":     "cand-1", // add new
	}))

	got, _, found, _, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "basic-agent", got.Name) // other keys preserved
	require.NotNil(t, got.EnvironmentVariables)
	_, hasInlineEnvironment := props.GetFields()["environmentVariables"]
	require.False(t, hasInlineEnvironment)

	values := AgentEnvironment(got)
	require.Equal(t, "gpt-4o", values["FOUNDRY_MODEL_DEPLOYMENT_NAME"])
	require.Equal(t, "cand-1", values["OPTIMIZATION_CANDIDATE_ID"])
}

func TestUpsertAgentEnvVarsPreservesNestedReferences(t *testing.T) {
	props, err := structpb.NewStruct(map[string]any{
		"kind": "hosted",
		"name": "basic-agent",
		"deployments": []any{
			map[string]any{"$ref": "./deployment.yaml"},
		},
	})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}

	require.NoError(t, UpsertAgentEnvVars(svc, map[string]string{
		"OPTIMIZATION_CANDIDATE_ID": "cand-1",
	}))

	deployments := svc.GetAdditionalProperties().GetFields()["deployments"]
	require.NotNil(t, deployments)
	require.Equal(
		t,
		"./deployment.yaml",
		deployments.GetListValue().GetValues()[0].
			GetStructValue().GetFields()["$ref"].GetStringValue(),
	)
}
