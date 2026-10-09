// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func preparePresencePreview(
	t *testing.T, service *azdext.ServiceConfig, root string,
) (*agent_api.CreateAgentRequest, previewInputs) {
	t.Helper()
	resolved, definition, err := resolvePreviewDefinition(service, root)
	require.NoError(t, err)
	request, inputs, err := preparePreviewRequest(resolved, definition, nil, nil)
	require.NoError(t, err)
	return request, inputs
}

func TestPreviewAbsentPropertiesAndDefaults(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "existing"}[existing], func(t *testing.T) {
			service := previewService(t)
			service.Image, service.Docker = "", nil
			service.AdditionalProperties.Fields["codeConfiguration"], _ = structpb.NewValue(map[string]any{
				"runtime": "python_3_13", "entryPoint": "app.py",
			})
			request, inputs := preparePresencePreview(t, service, t.TempDir())
			var remote *agent_api.AgentObject
			if existing {
				remote = remotePreviewAgent(request)
				remote.Versions.Latest.Metadata = map[string]string{"enableVnextExperience": "false"}
				hosted := request.Definition.(agent_api.HostedAgentDefinition)
				hosted.CPU, hosted.Memory = "2", "4Gi"
				hosted.ProtocolVersions = []agent_api.ProtocolVersionRecord{{Protocol: "a2a", Version: "1.0.0"}}
				remote.Versions.Latest.Definition = hosted
			}
			result, err := comparePreviewRequest(service.Name, request, remote, inputs)
			require.NoError(t, err)
			data := result.Data.AsMap()
			require.NotContains(t, data, "containerImage")
			for _, absent := range []string{
				"Protocols:", "Resources:", "Environment variables:", "Model deployment:", "Container image:",
				"metadata.enableVnextExperience", "description", "build:", "push:",
			} {
				require.NotContains(t, result.Message, absent)
			}
			if existing {
				require.Equal(t, "noChange", data["status"])
				require.Empty(t, data["changes"])
				require.NotContains(t, result.Message, "  Metadata:")
			} else {
				require.Equal(t, "create", data["status"])
				require.Equal(t, []any{map[string]any{
					"group": "metadata", "path": "name", "operation": "add", "after": "example-agent",
				}}, data["changes"])
			}
			// Normal deployment still receives the original normalization defaults.
			require.Equal(t, "true", request.Metadata["enableVnextExperience"])
			hosted := request.Definition.(agent_api.HostedAgentDefinition)
			require.Equal(t, "0.5", hosted.CPU)
			require.Equal(t, "1Gi", hosted.Memory)
			require.Len(t, hosted.ProtocolVersions, 1)
		})
	}
}

func TestPreviewReferencedPresenceAndDeclaredValues(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "fragment.yaml"),
		[]byte("description: \"\"\ncontainer:\n  resources:\n    $ref: resources.yaml\nmetadata:\n  empty: \"\"\n"+
			"protocols:\n  - protocol: responses\n    version: \"\"\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "resources.yaml"), []byte("cpu: \"0\"\n"), 0600))
	service := previewService(t)
	service.AdditionalProperties.Fields["$ref"] = structpb.NewStringValue("fragment.yaml")
	service.Environment = map[string]string{
		"EMPTY": "", "FALSE": "false", "ZERO": "0", "AZURE_AI_MODEL_DEPLOYMENT_NAME": "",
	}
	request, inputs := preparePresencePreview(t, service, root)
	require.Nil(t, request.Description, "ordinary deployment omits an empty description")
	result, err := comparePreviewRequest(service.Name, request, nil, inputs)
	require.NoError(t, err)
	for _, line := range []string{
		`add: description: ""`, `add: metadata.empty: ""`, `add: definition.cpu: "0"`,
		`add: definition.protocol_versions: [{"protocol":"responses","version":""}]`,
		`add: definition.environment_variables.EMPTY: ""`,
		`add: definition.environment_variables.FALSE: "[redacted]"`,
		`add: definition.environment_variables.ZERO: "[redacted]"`,
		`add: definition.environment_variables.AZURE_AI_MODEL_DEPLOYMENT_NAME: ""`,
		"build: false", "push: false",
	} {
		require.Contains(t, result.Message, line)
	}
	require.NotContains(t, result.Message, "definition.memory")
	require.NotContains(t, result.Message, "enableVnextExperience")
	changes, ok := result.Data.AsMap()["changes"].([]any)
	require.True(t, ok)
	for _, item := range changes {
		change, ok := item.(map[string]any)
		require.True(t, ok)
		require.Contains(t, change, "after")
		require.NotContains(t, change, "before")
	}
}

func TestPreviewOptionalProtocolVersionPresence(t *testing.T) {
	service := previewService(t)
	service.AdditionalProperties.Fields["protocols"], _ = structpb.NewValue([]any{
		map[string]any{"protocol": "responses"},
	})
	request, inputs := preparePresencePreview(t, service, t.TempDir())
	result, err := comparePreviewRequest(service.Name, request, nil, inputs)
	require.NoError(t, err)
	require.Contains(t, result.Message, `add: definition.protocol_versions: [{"protocol":"responses"}]`)
	require.NotContains(t, result.Message, `"version":`)
	encoded, err := json.Marshal(result.Data.AsMap())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"version":`)
	result, err = comparePreviewRequest(service.Name, request, remotePreviewAgent(request), inputs)
	require.NoError(t, err)
	require.Equal(t, "noChange", result.Data.AsMap()["status"])
	require.Empty(t, result.Data.AsMap()["changes"])
	require.NotContains(t, result.Data.AsMap(), "containerImage")
}

func TestPreviewAbsentDesiredPropertiesRemainRemovals(t *testing.T) {
	service := previewService(t)
	request, inputs := preparePresencePreview(t, service, t.TempDir())
	remote := remotePreviewAgent(request)
	remote.Versions.Latest.Description = new("private-description")
	remote.Versions.Latest.Metadata = maps.Clone(request.Metadata)
	remote.Versions.Latest.Metadata["removed"] = "private-tag"
	hosted := request.Definition.(agent_api.HostedAgentDefinition)
	hosted.EnvironmentVariables = map[string]string{
		"SECRET": "private-secret", "AZURE_AI_MODEL_DEPLOYMENT_NAME": "gpt-4.1",
	}
	container := *hosted.ContainerConfiguration
	container.RegistryConnectionID = "old-connection"
	hosted.ContainerConfiguration = &container
	remote.Versions.Latest.Definition = hosted
	result, err := comparePreviewRequest(service.Name, request, remote, inputs)
	require.NoError(t, err)
	require.Equal(t, "update", result.Data.AsMap()["status"])
	changes, ok := result.Data.AsMap()["changes"].([]any)
	require.True(t, ok)
	require.Len(t, changes, 5)
	for _, item := range changes {
		change, ok := item.(map[string]any)
		require.True(t, ok)
		require.Equal(t, "remove", change["operation"])
		require.Contains(t, change, "before")
		require.NotContains(t, change, "after")
		path, ok := change["path"].(string)
		require.True(t, ok)
		require.Contains(t, result.Message, "remove: "+path)
	}
	encoded, err := json.Marshal(result.Data.AsMap())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-")
	require.NotContains(t, result.Message, "private-")
	require.NotContains(t, result.Message, "enableVnextExperience")
}

func TestPreviewContainerIntentIsChangeOnly(t *testing.T) {
	for _, tc := range []struct {
		name       string
		create     bool
		build      bool
		changed    bool
		wantIntent bool
		wantStatus string
	}{
		{name: "create prebuilt", create: true, wantIntent: true, wantStatus: "create"},
		{name: "update prebuilt", changed: true, wantIntent: true, wantStatus: "update"},
		{name: "unchanged prebuilt", wantStatus: "noChange"},
		{name: "create container build", create: true, build: true, wantIntent: true, wantStatus: "create"},
		{name: "unchanged container build", build: true, wantStatus: "noChange"},
		{name: "resource change does not imply image change", build: true, changed: true, wantStatus: "update"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := previewService(t)
			if tc.build {
				service.Image = ""
				service.Docker.ImagePassthrough = false
				if tc.changed {
					service.AdditionalProperties.Fields["container"], _ = structpb.NewValue(map[string]any{
						"resources": map[string]any{"cpu": "2"},
					})
				}
			}
			request, inputs := preparePresencePreview(t, service, t.TempDir())
			remote := remotePreviewAgent(request)
			hosted := request.Definition.(agent_api.HostedAgentDefinition)
			container := *hosted.ContainerConfiguration
			container.Image = "registry.example.com/agent:old"
			if tc.build || tc.changed {
				hosted.ContainerConfiguration = &container
				if tc.build && tc.changed {
					hosted.CPU = "0.5"
				}
				remote.Versions.Latest.Definition = hosted
			}
			if tc.create {
				remote = nil
			}
			result, err := comparePreviewRequest(service.Name, request, remote, inputs)
			require.NoError(t, err)
			data := result.Data.AsMap()
			require.Equal(t, tc.wantStatus, data["status"])
			if tc.wantIntent {
				require.Equal(t, map[string]any{"build": tc.build, "push": tc.build}, data["containerImage"])
				require.Contains(t, result.Message, "  Container image:")
			} else {
				require.NotContains(t, data, "containerImage")
				require.NotContains(t, result.Message, "  Container image:")
			}
			require.NotContains(t, result.Message, "preview.invalid")
			if tc.changed && !tc.build {
				require.Contains(t, result.Message,
					`update: definition.container_configuration.image: "registry.example.com/agent:old" -> `+
						`"registry.example.com/agent:v1"`)
			}
		})
	}
}

func TestPreviewContainerRemovalHasNoSyntheticIntent(t *testing.T) {
	service := previewService(t)
	service.Image, service.Docker = "", nil
	service.AdditionalProperties.Fields["codeConfiguration"], _ = structpb.NewValue(map[string]any{
		"runtime": "python_3_13", "entryPoint": "app.py",
	})
	request, inputs := preparePresencePreview(t, service, t.TempDir())
	remote := remotePreviewAgent(previewRequest(t))
	result, err := comparePreviewRequest(service.Name, request, remote, inputs)
	require.NoError(t, err)
	require.Equal(t, "update", result.Data.AsMap()["status"])
	require.Contains(t, result.Message,
		`remove: definition.container_configuration.image: "registry.example.com/agent:v1" -> (removed)`)
	require.NotContains(t, result.Data.AsMap(), "containerImage")
	require.NotContains(t, result.Message, "build:")
	require.NotContains(t, result.Message, "push:")
}

func TestPreviewChangeJSONPreservesExplicitFalsyValues(t *testing.T) {
	for _, value := range []any{false, float64(0), ""} {
		result, err := previewResponse(agentPreviewResult{Changes: []previewChange{
			{Group: "metadata", Path: "metadata.value", Operation: "update", Before: "old", After: value},
		}})
		require.NoError(t, err)
		changes, ok := result.Data.AsMap()["changes"].([]any)
		require.True(t, ok)
		change, ok := changes[0].(map[string]any)
		require.True(t, ok)
		require.Contains(t, change, "after")
		require.Equal(t, value, change["after"])
		require.NotContains(t, result.Data.AsMap(), "containerImage")
	}
}
