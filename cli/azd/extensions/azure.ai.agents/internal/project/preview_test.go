// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func previewService(t *testing.T) *azdext.ServiceConfig {
	t.Helper()
	props, err := structpb.NewStruct(map[string]any{"kind": "hosted", "name": "example-agent"})
	require.NoError(t, err)
	return &azdext.ServiceConfig{
		Name: "agent", Host: foundryAgentHost, AdditionalProperties: props,
		Image: "registry.example.com/agent:v1", Docker: &azdext.DockerProjectOptions{ImagePassthrough: true},
	}
}

func previewRequest(t *testing.T) *agent_api.CreateAgentRequest {
	t.Helper()
	service := previewService(t)
	definition, _, _, _, err := AgentDefinitionFromService(service)
	require.NoError(t, err)
	request, _, err := preparePreviewRequest(service, definition, nil, nil)
	require.NoError(t, err)
	return request
}

func remotePreviewAgent(request *agent_api.CreateAgentRequest) *agent_api.AgentObject {
	agent := &agent_api.AgentObject{
		Name: request.Name, AgentEndpoint: request.AgentEndpoint, AgentCard: request.AgentCard,
		DigitalWorkerType: request.DigitalWorkerType,
	}
	agent.Versions.Latest = agent_api.AgentVersionObject{
		Version: "1", Name: request.Name, Definition: request.Definition,
		Description: request.Description, Metadata: request.Metadata,
	}
	return agent
}

type recordingPreviewReader struct {
	agent   *agent_api.AgentObject
	err     error
	calls   int
	version string
}

func (r *recordingPreviewReader) GetAgent(
	_ context.Context, _ string, version string, _ bool,
) (*agent_api.AgentObject, error) {
	r.calls++
	r.version = version
	return r.agent, r.err
}

func TestPreviewDefinitionSources(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*azdext.ServiceConfig, string)
		code   string
	}{
		{name: "unified"},
		{name: "unused legacy file", change: func(_ *azdext.ServiceConfig, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"), []byte("invalid: ["), 0600))
		}},
		{name: "field reference", change: func(service *azdext.ServiceConfig, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "metadata.yaml"), []byte("owner: team\n"), 0600))
			service.AdditionalProperties.Fields["metadata"], _ = structpb.NewValue(map[string]any{"$ref": "metadata.yaml"})
		}},
		{name: "root fragment", change: func(service *azdext.ServiceConfig, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "fragment.yaml"), []byte("description: included\n"), 0600))
			service.AdditionalProperties.Fields["$ref"] = structpb.NewStringValue("fragment.yaml")
		}},
		{name: "nested config", change: func(service *azdext.ServiceConfig, _ string) {
			service.Config, service.AdditionalProperties = service.AdditionalProperties, nil
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "unused nested config", change: func(service *azdext.ServiceConfig, _ string) {
			service.Config, _ = structpb.NewStruct(map[string]any{"$ref": "missing.yaml"})
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "whole definition ref", change: func(service *azdext.ServiceConfig, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "custom.yaml"),
				[]byte("kind: hosted\nname: from-file\n"), 0600))
			service.AdditionalProperties.Fields["$ref"] = structpb.NewStringValue("custom.yaml")
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "transitive manifest", change: func(service *azdext.ServiceConfig, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.manifest.yaml"),
				[]byte("template:\n  kind: hosted\n  name: from-file\n"), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "wrapper.yaml"),
				[]byte("$ref: agent.manifest.yaml\n"), 0600))
			service.AdditionalProperties.Fields["$ref"] = structpb.NewStringValue("wrapper.yaml")
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "missing kind", change: func(service *azdext.ServiceConfig, _ string) {
			delete(service.AdditionalProperties.Fields, "kind")
		}, code: exterrors.CodeInvalidServiceConfig},
		{name: "missing name", change: func(service *azdext.ServiceConfig, _ string) {
			delete(service.AdditionalProperties.Fields, "name")
		}, code: exterrors.CodeInvalidServiceConfig},
		{name: "prompt", change: func(service *azdext.ServiceConfig, _ string) {
			service.AdditionalProperties.Fields["kind"] = structpb.NewStringValue("prompt")
		}, code: exterrors.CodeUnsupportedAgentKind},
		{name: "voice", change: func(service *azdext.ServiceConfig, _ string) {
			service.AdditionalProperties.Fields["kind"] = structpb.NewStringValue("voice")
		}, code: exterrors.CodeUnsupportedAgentKind},
		{name: "bad reference", change: func(service *azdext.ServiceConfig, _ string) {
			service.AdditionalProperties.Fields["metadata"], _ = structpb.NewValue(map[string]any{"$ref": "missing.yaml"})
		}, code: exterrors.CodeInvalidServiceConfig},
		{name: "credential-bearing invalid image", change: func(service *azdext.ServiceConfig, _ string) {
			service.Image = "https://private-user:private-password@host/agent?sig=private-signature#private-fragment"
		}, code: exterrors.CodeInvalidServiceConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENT_DEFINITION_PATH", "")
			root := t.TempDir()
			service := previewService(t)
			if tc.change != nil {
				tc.change(service, root)
			}
			before := proto.CloneOf(service)
			resolved, definition, err := resolvePreviewDefinition(service, root)
			require.True(t, proto.Equal(before, service))
			if tc.code != "" {
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, tc.code, local.Code)
				require.NotContains(t, err.Error(), "private-")
				return
			}
			require.NoError(t, err)
			require.Equal(t, "example-agent", definition.Name)
			require.NotSame(t, service, resolved)
		})
	}
	for _, filename := range []string{"agent.yaml", "agent.yml", "agent.manifest.yaml", "agent.manifest.yml"} {
		t.Run(filename, func(t *testing.T) {
			t.Setenv("AGENT_DEFINITION_PATH", "")
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, filename), []byte("legacy: true\n"), 0600))
			service := previewService(t)
			service.AdditionalProperties = nil
			_, _, err := resolvePreviewDefinition(service, root)
			require.ErrorContains(t, err, "unsupported for legacy")
		})
	}
}

func TestPreviewRejectsDefinitionOverride(t *testing.T) {
	t.Setenv("AGENT_DEFINITION_PATH", "private-path-not-read.yaml")
	_, _, err := resolvePreviewDefinition(previewService(t), t.TempDir())
	require.ErrorContains(t, err, "AGENT_DEFINITION_PATH")
	require.NotContains(t, err.Error(), "private-path")
}

func TestPreviewRequestDeploymentParity(t *testing.T) {
	for _, mode := range []string{"passthrough", "build", "code", "legacy image marker"} {
		t.Run(mode, func(t *testing.T) {
			service := previewService(t)
			service.Environment = map[string]string{
				"FORWARDED": "$literal", "AZURE_AI_MODEL_DEPLOYMENT_NAME": "model",
			}
			props := service.AdditionalProperties.Fields
			props["metadata"], _ = structpb.NewValue(map[string]any{"owner": "team", "authors": []any{"developer"}})
			props["container"], _ = structpb.NewValue(map[string]any{
				"resources": map[string]any{"cpu": "2", "memory": "4Gi"},
			})
			props["protocols"], _ = structpb.NewValue([]any{
				map[string]any{"protocol": "responses", "version": "2.0.0"},
				map[string]any{"protocol": "a2a", "version": "1.0.0"},
			})
			environment := map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://host"}
			switch mode {
			case "build":
				service.Image = ""
				service.Docker.ImagePassthrough = false
			case "code":
				props["codeConfiguration"], _ = structpb.NewValue(map[string]any{
					"runtime": "python_3_13", "entryPoint": "app.py",
				})
			case "legacy image marker":
				service.Docker.ImagePassthrough = false
				environment["AZD_AGENT_SKIP_ACR"] = "true"
			}
			definition, _, _, _, err := AgentDefinitionFromService(service)
			require.NoError(t, err)
			request, unknown, err := preparePreviewRequest(service, definition, environment, nil)
			require.NoError(t, err)
			var options []agent_yaml.AgentBuildOption
			if mode == "build" {
				options = append(options, agent_yaml.WithImageURL("preview.invalid/unknown"))
			}
			deployed, err := (&AgentServiceTargetProvider{}).prepareDeploy(service, definition, environment, options)
			require.NoError(t, err)
			require.Equal(t, deployed.request, request)
			hosted, ok := request.Definition.(agent_api.HostedAgentDefinition)
			require.True(t, ok)
			require.Equal(t, "$literal", hosted.EnvironmentVariables["FORWARDED"])
			require.Equal(t, "2", hosted.CPU)
			require.Equal(t, "4Gi", hosted.Memory)
			require.Equal(t, "true", request.Metadata["enableVnextExperience"])
			if mode == "build" || mode == "code" {
				require.Len(t, unknown, 1)
			} else {
				require.Empty(t, unknown)
			}
		})
	}
}

func TestPreviewRemoteOutcomes(t *testing.T) {
	request := previewRequest(t)
	for _, tc := range []struct {
		name    string
		agent   *agent_api.AgentObject
		err     error
		status  string
		unknown []string
	}{
		{name: "create", err: &azcore.ResponseError{StatusCode: http.StatusNotFound}, status: "create"},
		{name: "no change", agent: remotePreviewAgent(request), status: "noChange"},
		{name: "unknown image", agent: remotePreviewAgent(request), status: "unknown", unknown: []string{previewImagePath}},
		{name: "unknown code", agent: remotePreviewAgent(request), status: "unknown", unknown: []string{"codeArtifact"}},
		{name: "unauthorized", err: &azcore.ResponseError{StatusCode: http.StatusUnauthorized}},
		{name: "forbidden", err: &azcore.ResponseError{
			StatusCode: http.StatusForbidden, ErrorCode: "private-response-body",
		}},
		{name: "transport credentials", err: fmt.Errorf("https://user:private-password@host?sig=private-signature")},
		{name: "nil response"},
		{name: "empty response", agent: &agent_api.AgentObject{}},
		{name: "no latest", agent: &agent_api.AgentObject{Name: request.Name}},
		{name: "bad definition", agent: remotePreviewAgent(&agent_api.CreateAgentRequest{
			Name: request.Name, CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{Definition: "private-bad"},
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &recordingPreviewReader{agent: tc.agent, err: tc.err}
			result, err := previewAgentRequest(t.Context(), reader, "agent", request, tc.unknown)
			require.Equal(t, 1, reader.calls)
			require.Equal(t, agent_api.AgentEndpointAPIVersion, reader.version)
			if tc.status == "" {
				require.Error(t, err)
				require.Nil(t, result)
				require.NotContains(t, err.Error(), "private-")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.status, result.Data.AsMap()["status"])
			if len(tc.unknown) > 0 {
				require.Contains(t, result.Message, "unknown")
				require.NotContains(t, result.Message, "No deployment configuration changes")
			}
		})
	}
}

func TestPreviewGroupedChangesAndNonDisclosure(t *testing.T) {
	request := previewRequest(t)
	remote := remotePreviewAgent(request)
	hosted := request.Definition.(agent_api.HostedAgentDefinition)
	hosted.EnvironmentVariables = map[string]string{
		"API_KEY": "private-local-secret", "AZURE_AI_MODEL_DEPLOYMENT_NAME": "gpt-4.1",
	}
	hosted.CPU = "2"
	hosted.Memory = "4Gi"
	hosted.ProtocolVersions = []agent_api.ProtocolVersionRecord{{Protocol: "a2a", Version: "1.0.0"}}
	hosted.ContainerConfiguration = &agent_api.ContainerConfigurationAPI{Image: "registry.example.com/agent:v2"}
	request.Definition = hosted
	request.Description = new("https://private-user:private-password@host/path?sig=private-signature#private-fragment")
	request.Metadata = map[string]string{"owner": "private-local-secret"}
	oldHosted := remote.Versions.Latest.Definition.(agent_api.HostedAgentDefinition)
	oldHosted.EnvironmentVariables = map[string]string{
		"REMOVED_SECRET": "private-remote-secret", "AZURE_AI_MODEL_DEPLOYMENT_NAME": "gpt-4o",
	}
	remote.Versions.Latest.Definition = oldHosted
	result, err := comparePreviewRequest("agent", request, remote, nil)
	require.NoError(t, err)
	require.Equal(t, "update", result.Data.AsMap()["status"])
	for _, label := range []string{
		"Metadata", "Protocols", "Resources", "Environment variables", "Model deployment", "Container image",
	} {
		require.Contains(t, result.Message, "  "+label+":\n")
	}
	require.Contains(t, result.Message, "remove: definition.environment_variables.REMOVED_SECRET")
	var parsed agentPreviewResult
	encoded, err := json.Marshal(result.Data.AsMap())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &parsed))
	require.NotEmpty(t, parsed.Changes)
	require.NotContains(t, result.Message, "private-")
	require.NotContains(t, string(encoded), "private-")
	require.Contains(t, result.Message, `update: definition.cpu: "0.5" -> "2"`)
	require.Contains(t, result.Message, `add: definition.environment_variables.API_KEY: "[redacted]"`)
	require.Contains(t, result.Message,
		`remove: definition.environment_variables.REMOVED_SECRET: "[redacted]" -> (removed)`)
	require.Contains(t, result.Message,
		`update: definition.environment_variables.AZURE_AI_MODEL_DEPLOYMENT_NAME: "gpt-4o" -> "gpt-4.1"`)
	for _, change := range parsed.Changes {
		switch change.Path {
		case "definition.cpu":
			require.Equal(t, "0.5", change.Before)
			require.Equal(t, "2", change.After)
		case "definition.environment_variables.API_KEY", "metadata.owner", "description":
			require.Equal(t, previewRedactedValue, change.After)
		case "definition.environment_variables.REMOVED_SECRET":
			require.Equal(t, previewRedactedValue, change.Before)
			require.Nil(t, change.After)
		}
	}
}

func TestPreviewCreateIncludesSafeValues(t *testing.T) {
	service := previewService(t)
	service.AdditionalProperties.Fields["codeConfiguration"], _ = structpb.NewValue(map[string]any{
		"runtime": "python_3_13", "entryPoint": "app.py",
	})
	service.Environment = map[string]string{
		"AZURE_AI_MODEL_DEPLOYMENT_NAME": "gpt-4.1", "API_KEY": "private-secret",
		"NUMBER": "0.5", "FLAG": "true",
	}
	_, definition, err := resolvePreviewDefinition(service, t.TempDir())
	require.NoError(t, err)
	request, unknown, err := preparePreviewRequest(service, definition, nil, nil)
	require.NoError(t, err)
	request.Description = new("private-description")
	request.Metadata["arbitrary"] = "private-metadata"
	request.AgentEndpoint = &agent_api.AgentEndpoint{
		ProtocolConfiguration: &agent_api.ProtocolConfiguration{
			Activity: &agent_api.ActivityProtocolConfiguration{EnableM365PublicEndpoint: new(false)},
		},
		VersionSelector: &agent_api.VersionSelector{VersionSelectionRules: []agent_api.VersionSelectionRule{
			{Type: agent_api.VersionSelectorTypeFixedRatio, AgentVersion: "1", TrafficPercentage: new(int32(0))},
		}},
	}
	result, err := comparePreviewRequest(service.Name, request, nil, unknown)
	require.NoError(t, err)
	require.Equal(t, "create", result.Data.AsMap()["status"])
	for _, line := range []string{
		`add: definition.kind: "hosted"`,
		`add: name: "example-agent"`,
		`add: metadata.enableVnextExperience: "true"`,
		`add: definition.cpu: "0.5"`,
		`add: definition.memory: "1Gi"`,
		`add: definition.protocol_versions: [{"protocol":"responses","version":"2.0.0"}]`,
		`add: definition.code_configuration.runtime: "python_3_13"`,
		`add: definition.code_configuration.entry_point: ["python","app.py"]`,
		`add: definition.code_configuration.dependency_resolution: "remote_build"`,
		`add: definition.session_configuration.idle_timeout_seconds: 900`,
		`add: definition.environment_variables.AZURE_AI_MODEL_DEPLOYMENT_NAME: "gpt-4.1"`,
		`add: definition.environment_variables.API_KEY: "[redacted]"`,
		`add: definition.environment_variables.NUMBER: "[redacted]"`,
		`add: description: "[redacted]"`,
		`add: metadata.arbitrary: "[redacted]"`,
		`add: agent_endpoint.protocol_configuration.activity.enable_m365_public_endpoint: false`,
		`add: agent_endpoint.version_selector.version_selection_rules: ` +
			`[{"agent_version":"1","traffic_percentage":0,"type":"FixedRatio"}]`,
		`unknown: codeArtifact`,
	} {
		require.Contains(t, result.Message, line)
	}
	require.NotContains(t, result.Message, "private-")
	require.NotContains(t, result.Message, "preview.invalid")
	data := result.Data.AsMap()
	changes, ok := data["changes"].([]any)
	require.True(t, ok)
	for _, item := range changes {
		change, ok := item.(map[string]any)
		require.True(t, ok)
		require.Equal(t, "add", change["operation"])
		require.NotContains(t, change, "before")
		require.Contains(t, change, "after")
		encoded, err := json.Marshal(change["after"])
		require.NoError(t, err)
		require.Contains(t, result.Message, fmt.Sprintf("add: %s: %s", change["path"], encoded))
	}
	encoded, err := json.Marshal(data)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-")
	require.NotContains(t, string(encoded), "preview.invalid")
}

func TestPreviewValueRedactionPolicy(t *testing.T) {
	clean := func(value string) string { return redactPreviewURLs(value) }
	imageDigest := "registry.example.com/agent@sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		path  string
		value any
		want  any
	}{
		{path: previewImagePath, value: imageDigest, want: imageDigest},
		//nolint:gosec // Fake credential-bearing URL verifies non-disclosure.
		{path: previewImagePath, value: "https://user:private-password@host/image?sig=private-sas#private-fragment",
			want: "https://host/image"},
		{path: "definition.environment_variables.PASSWORD", value: "private-password", want: previewRedactedValue},
		{path: "definition.environment_variables.CONFIG", value: `{"password":"private-json"}`, want: previewRedactedValue},
		{path: "metadata.token", value: "private-metadata", want: previewRedactedValue},
		{path: "agent_card.skills", value: []any{"private-card-content"}, want: previewRedactedValue},
		{path: "definition.code_configuration.runtime", value: "private-runtime", want: previewRedactedValue},
		{path: "definition.code_configuration.entry_point",
			value: []any{"python", "-c", "private-code"}, want: previewRedactedValue},
		{path: "definition.code_configuration.entry_point",
			value: []any{"python", "app.py --token=private-token"}, want: previewRedactedValue},
		{path: "definition.protocol_versions", value: []any{
			map[string]any{"protocol": "responses", "version": "private-version"},
		}, want: []any{map[string]any{"protocol": "responses", "version": previewRedactedValue}}},
		{path: "agent_endpoint.authorization_schemes", value: []any{
			map[string]any{"type": "Entra", "isolation_key_source": map[string]any{"kind": "Header"}},
		}, want: []any{map[string]any{"type": "Entra", "isolation_key_source": map[string]any{"kind": "Header"}}}},
		{path: "agent_endpoint.protocol_configuration.activity.enable_m365_public_endpoint", value: false, want: false},
		{path: "agent_endpoint.version_selector.version_selection_rules.traffic_percentage", value: float64(0),
			want: float64(0)},
		{path: "definition.future_api_field", value: "private-future-value", want: previewRedactedValue},
	} {
		t.Run(tc.path, func(t *testing.T) {
			actual := previewDisplayValue(tc.path, tc.value, clean)
			require.Equal(t, tc.want, actual)
			data, err := json.Marshal(actual)
			require.NoError(t, err)
			require.NotContains(t, string(data), "private-")
		})
	}
}

func TestPreviewSafeRemovalAndEmptyValue(t *testing.T) {
	request := previewRequest(t)
	remote := remotePreviewAgent(request)
	hosted := remote.Versions.Latest.Definition.(agent_api.HostedAgentDefinition)
	container := *hosted.ContainerConfiguration
	container.RegistryConnectionID = "registry-connection"
	hosted.ContainerConfiguration = &container
	hosted.EnvironmentVariables = map[string]string{"AZURE_AI_MODEL_DEPLOYMENT_NAME": "gpt-4.1"}
	remote.Versions.Latest.Definition = hosted
	desired := request.Definition.(agent_api.HostedAgentDefinition)
	desired.EnvironmentVariables = map[string]string{"AZURE_AI_MODEL_DEPLOYMENT_NAME": ""}
	request.Definition = desired
	result, err := comparePreviewRequest("agent", request, remote, nil)
	require.NoError(t, err)
	require.Contains(t, result.Message,
		`remove: definition.container_configuration.registry_connection_id: "registry-connection" -> (removed)`)
	require.Contains(t, result.Message,
		`update: definition.environment_variables.AZURE_AI_MODEL_DEPLOYMENT_NAME: "gpt-4.1" -> ""`)
	changes, ok := result.Data.AsMap()["changes"].([]any)
	require.True(t, ok)
	require.Len(t, changes, 2)
	for _, item := range changes {
		change, ok := item.(map[string]any)
		require.True(t, ok)
		if change["operation"] == "remove" {
			require.Equal(t, "registry-connection", change["before"])
			require.NotContains(t, change, "after")
		} else {
			require.Contains(t, change, "after")
			require.Equal(t, "", change["after"])
		}
	}
}

func TestPreviewNormalizationAndPatchPreservation(t *testing.T) {
	request := previewRequest(t)
	hosted := request.Definition.(agent_api.HostedAgentDefinition)
	hosted.CPU = "1.0"
	hosted.ProtocolVersions = []agent_api.ProtocolVersionRecord{
		{Protocol: "responses", Version: "2.0.0"}, {Protocol: "a2a", Version: "1.0.0"},
	}
	request.Definition = hosted
	request.AgentEndpoint = &agent_api.AgentEndpoint{
		Protocols: []agent_api.AgentEndpointProtocol{"responses", "a2a"},
	}
	remote := remotePreviewAgent(request)
	hosted.CPU = "1"
	hosted.SessionConfiguration = &agent_api.SessionConfigurationAPI{IdleTimeoutSeconds: 900}
	hosted.ProtocolVersions = []agent_api.ProtocolVersionRecord{
		{Protocol: "a2a", Version: "1.0.0"}, {Protocol: "responses", Version: "2.0.0"},
		{Protocol: "responses", Version: "2.0.0"},
	}
	hosted.Image = hosted.ContainerConfiguration.Image
	remote.Versions.Latest.Definition = hosted
	remote.AgentEndpoint = &agent_api.AgentEndpoint{
		Protocols: []agent_api.AgentEndpointProtocol{"a2a", "responses"},
		VersionSelector: &agent_api.VersionSelector{VersionSelectionRules: []agent_api.VersionSelectionRule{
			{Type: agent_api.VersionSelectorTypeFixedRatio, AgentVersion: "1", TrafficPercentage: new(int32(100))},
		}},
	}
	before, err := json.Marshal(request)
	require.NoError(t, err)
	result, err := comparePreviewRequest("agent", request, remote, nil)
	require.NoError(t, err)
	require.Equal(t, "noChange", result.Data.AsMap()["status"])
	after, err := json.Marshal(request)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestPreviewCodeAndBuildArtifactsUnknown(t *testing.T) {
	for _, mode := range []string{"build", "ambiguous image", "code"} {
		t.Run(mode, func(t *testing.T) {
			service := previewService(t)
			service.Docker.ImagePassthrough = false
			if mode == "build" {
				service.Image = ""
			}
			if mode == "code" {
				service.AdditionalProperties.Fields["codeConfiguration"], _ = structpb.NewValue(map[string]any{
					"runtime": "python_3_13", "entryPoint": "app.py",
				})
			}
			definition, _, _, _, err := AgentDefinitionFromService(service)
			require.NoError(t, err)
			request, unknown, err := preparePreviewRequest(service, definition, nil, nil)
			require.NoError(t, err)
			remote := remotePreviewAgent(request)
			hosted := request.Definition.(agent_api.HostedAgentDefinition)
			hosted.ContainerConfiguration = &agent_api.ContainerConfigurationAPI{Image: "registry.example.com/built:v9"}
			if mode == "code" {
				code := *hosted.CodeConfiguration
				code.DependencyResolution = ""
				hosted.CodeConfiguration = &code
			}
			remote.Versions.Latest.Definition = hosted
			result, err := comparePreviewRequest("agent", request, remote, unknown)
			require.NoError(t, err)
			require.Equal(t, "unknown", result.Data.AsMap()["status"])
			require.Empty(t, result.Data.AsMap()["changes"])
			require.NotContains(t, result.Message, "preview.invalid")
		})
	}
}

func TestPreviewPendingEnvironmentAndImage(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(`
name: preview
services:
  agent:
    image: ${PREVIEW_UNSET_IMAGE}
    env:
      MODEL: ${PREVIEW_UNSET_MODEL}
      PARTIAL: prefix-${PREVIEW_UNSET_MODEL}
      DEFAULT: ${PREVIEW_UNSET_DEFAULT:-fallback}
      EMPTY_DEFAULT: ${PREVIEW_UNSET_DEFAULT:-}
      LITERAL: ""
      TEMPLATE: ${{connections.example.credentials.key}}
`), 0600))
	service := previewService(t)
	service.Image = ""
	pending, err := previewPendingInputs(root, service, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		previewImagePath, "definition.environment_variables.MODEL", "definition.environment_variables.PARTIAL",
	}, pending)
	definition, _, _, _, err := AgentDefinitionFromService(service)
	require.NoError(t, err)
	request, unknown, err := preparePreviewRequest(service, definition, nil, pending)
	require.NoError(t, err)
	require.ElementsMatch(t, pending, unknown)
	result, err := comparePreviewRequest("agent", request, remotePreviewAgent(previewRequest(t)), unknown)
	require.NoError(t, err)
	require.Equal(t, "unknown", result.Data.AsMap()["status"])
	require.Empty(t, result.Data.AsMap()["changes"])
}

func TestPreviewEnvironmentExpansion(t *testing.T) {
	for _, tc := range []struct {
		expression string
		unknown    bool
		invalid    bool
	}{
		{expression: "${MISSING}", unknown: true},
		{expression: "prefix-${MISSING}", unknown: true},
		{expression: "${MISSING:-fallback}"},
		{expression: "${MISSING:-}"},
		{expression: "${{connections.example.credentials.key}}"},
		{expression: "${{event.${MISSING}}}"},
		{expression: "${MISSING", invalid: true},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			service := previewService(t)
			definition, _, _, _, err := AgentDefinitionFromService(service)
			require.NoError(t, err)
			definition.EnvironmentVariables = &[]agent_yaml.EnvironmentVariable{{Name: "VALUE", Value: tc.expression}}
			request, unknown, err := preparePreviewRequest(service, definition, nil, nil)
			if tc.invalid {
				require.Error(t, err)
				require.Nil(t, request)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.unknown, len(unknown) > 0)
		})
	}
}

func TestPreviewPrivateRegistryPendingImage(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(`
name: preview
services:
  agent:
    image: ${PREVIEW_UNSET_PRIVATE_IMAGE}
`), 0600))
	service := previewService(t)
	service.Image = ""
	service.AdditionalProperties.Fields["registryConnectionId"] = structpb.NewStringValue("private-registry")
	before := proto.CloneOf(service)
	service, definition, err := resolvePreviewDefinition(service, root)
	require.NoError(t, err)
	pending, err := previewPendingInputs(root, service, nil)
	require.NoError(t, err)
	request, unknown, err := preparePreviewRequest(service, definition, nil, pending)
	require.NoError(t, err)
	require.True(t, proto.Equal(before, service))
	require.Contains(t, unknown, previewImagePath)
	remote := remotePreviewAgent(request)
	hosted := request.Definition.(agent_api.HostedAgentDefinition)
	container := *hosted.ContainerConfiguration
	container.Image = "registry.example.com/agent:v1"
	hosted.ContainerConfiguration = &container
	remote.Versions.Latest.Definition = hosted
	result, err := comparePreviewRequest("agent", request, remote, unknown)
	require.NoError(t, err)
	require.Equal(t, "unknown", result.Data.AsMap()["status"])
	require.NotContains(t, result.Message, "preview.invalid")

	service.Docker.RemoteBuild = true
	_, _, err = preparePreviewRequest(service, definition, nil, pending)
	require.Error(t, err)
}

func TestPreviewURLRedactionAndWriter(t *testing.T) {
	//nolint:gosec // Fake credential-bearing URL for non-disclosure coverage.
	raw := "https://private-user:private-password@host/path?sig=private-signature#private-fragment"
	endpoint, err := previewProjectEndpoint(raw)
	require.NoError(t, err)
	require.Equal(t, "https://host/path", endpoint)
	require.Equal(t, "at https://host/path", redactPreviewURLs("at "+raw))
	for _, invalid := range []string{raw + "%invalid", "http://host?sig=private-signature"} {
		_, err := previewProjectEndpoint(invalid)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private-")
	}
	request := previewRequest(t)
	request.Metadata[raw] = "not emitted"
	result, err := comparePreviewRequest("agent", request, nil, nil)
	require.NoError(t, err)
	require.NotContains(t, result.Message, "private-")
	encoded, err := json.Marshal(result.Data.AsMap())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-")
	remote := remotePreviewAgent(request)
	hosted := remote.Versions.Latest.Definition.(agent_api.HostedAgentDefinition)
	container := *hosted.ContainerConfiguration
	container.Image = raw
	hosted.ContainerConfiguration = &container
	remote.Versions.Latest.Definition = hosted
	result, err = comparePreviewRequest("agent", request, remote, nil)
	require.NoError(t, err)
	require.Contains(t, result.Message,
		`update: definition.container_configuration.image: "https://host/path" -> "registry.example.com/agent:v1"`)
	encoded, err = json.Marshal(result.Data.AsMap())
	require.NoError(t, err)
	require.NotContains(t, result.Message, "private-")
	require.NotContains(t, string(encoded), "private-")
	request.Metadata["escape\x1b[31m\u009b"] = "not emitted"
	result, err = comparePreviewRequest("agent", request, nil, nil)
	require.NoError(t, err)
	require.NotContains(t, result.Message, "\x1b")
	require.NotContains(t, result.Message, "\u009b")
	var buffer bytes.Buffer
	require.NoError(t, writeAgentPreview(&buffer, agentPreviewResult{Status: "create"}))
	require.Contains(t, buffer.String(), "Would create")
	require.ErrorIs(t, writeAgentPreview(failingPreviewWriter{}, agentPreviewResult{}), errPreviewWrite)
}

var errPreviewWrite = errors.New("writer failed")

type failingPreviewWriter struct{}

func (failingPreviewWriter) Write([]byte) (int, error) { return 0, errPreviewWrite }
