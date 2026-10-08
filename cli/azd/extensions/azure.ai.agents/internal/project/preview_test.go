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
	"regexp"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func previewService(t *testing.T) *azdext.ServiceConfig {
	t.Helper()
	props, err := structpb.NewStruct(map[string]any{
		"kind": "hosted", "name": "example-agent",
	})
	require.NoError(t, err)
	return &azdext.ServiceConfig{
		Name: "agent", Host: "azure.ai.agent", AdditionalProperties: props,
		Image:  "registry.example.com/agent:v1",
		Docker: &azdext.DockerProjectOptions{ImagePassthrough: true},
	}
}

func TestPreviewDefinitionSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*azdext.ServiceConfig, string)
		code   string
	}{
		{name: "modern"},
		{name: "unused legacy file", change: func(_ *azdext.ServiceConfig, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"), []byte("invalid: ["), 0600))
		}},
		{name: "unused nested ref", change: func(s *azdext.ServiceConfig, _ string) {
			s.Config, _ = structpb.NewStruct(map[string]any{"$ref": "missing.yaml"})
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "field include", change: func(s *azdext.ServiceConfig, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "metadata.yaml"), []byte("owner: example\n"), 0600))
			ref, _ := structpb.NewStruct(map[string]any{"$ref": "metadata.yaml"})
			s.AdditionalProperties.Fields["metadata"] = structpb.NewStructValue(ref)
		}},
		{name: "root fragment", change: func(s *azdext.ServiceConfig, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "fragment.yaml"), []byte("description: included\n"), 0600))
			s.AdditionalProperties.Fields["$ref"] = structpb.NewStringValue("fragment.yaml")
		}},
		{name: "nested config", change: func(s *azdext.ServiceConfig, _ string) {
			s.Config = s.AdditionalProperties
			s.AdditionalProperties = nil
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "irrelevant inline fields do not override nested", change: func(s *azdext.ServiceConfig, _ string) {
			s.Config = s.AdditionalProperties
			s.AdditionalProperties, _ = structpb.NewStruct(map[string]any{"container": map[string]any{}})
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "legacy yaml", change: func(s *azdext.ServiceConfig, root string) {
			s.AdditionalProperties = nil
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"), []byte("kind: hosted\n"), 0600))
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "legacy yml", change: func(s *azdext.ServiceConfig, root string) {
			s.AdditionalProperties = nil
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yml"), []byte("kind: hosted\n"), 0600))
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "legacy manifest", change: func(s *azdext.ServiceConfig, root string) {
			s.AdditionalProperties = nil
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.manifest.yaml"), []byte("template: {}\n"), 0600))
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "legacy yml manifest", change: func(s *azdext.ServiceConfig, root string) {
			s.AdditionalProperties = nil
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.manifest.yml"), []byte("template: {}\n"), 0600))
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "whole manifest reference", change: func(s *azdext.ServiceConfig, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.manifest.yaml"),
				[]byte("template:\n  kind: hosted\n  name: legacy\n"), 0600))
			s.AdditionalProperties.Fields["$ref"] = structpb.NewStringValue("agent.manifest.yaml")
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "whole definition reference with inline kind", change: func(s *azdext.ServiceConfig, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"),
				[]byte("kind: hosted\nname: from-file\n"), 0600))
			s.AdditionalProperties.Fields["$ref"] = structpb.NewStringValue("agent.yaml")
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "transitive whole definition", change: func(s *azdext.ServiceConfig, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yml"), []byte("kind: hosted\nname: file\n"), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "wrapper.yaml"), []byte("$ref: agent.yml\n"), 0600))
			s.AdditionalProperties.Fields["$ref"] = structpb.NewStringValue("wrapper.yaml")
		}, code: exterrors.CodeDeploymentPreviewUnsupported},
		{name: "missing modern definition", change: func(s *azdext.ServiceConfig, _ string) {
			s.AdditionalProperties = nil
		}, code: exterrors.CodeInvalidServiceConfig},
		{name: "missing kind", change: func(s *azdext.ServiceConfig, _ string) {
			delete(s.AdditionalProperties.Fields, "kind")
		}, code: exterrors.CodeInvalidServiceConfig},
		{name: "empty name is not a default", change: func(s *azdext.ServiceConfig, _ string) {
			s.AdditionalProperties.Fields["name"] = structpb.NewStringValue("")
		}, code: exterrors.CodeInvalidServiceConfig},
		{name: "name cannot inject request URL credentials", change: func(s *azdext.ServiceConfig, _ string) {
			s.AdditionalProperties.Fields["name"] = structpb.NewStringValue("agent?sig=private-secret")
		}, code: exterrors.CodeInvalidServiceConfig},
		{name: "invalid kind type", change: func(s *azdext.ServiceConfig, _ string) {
			s.AdditionalProperties.Fields["kind"] = structpb.NewNumberValue(12)
		}, code: exterrors.CodeInvalidServiceConfig},
		{name: "prompt is unsupported", change: func(s *azdext.ServiceConfig, _ string) {
			s.AdditionalProperties.Fields["kind"] = structpb.NewStringValue("prompt")
		}, code: exterrors.CodeUnsupportedAgentKind},
		{name: "bad field include", change: func(s *azdext.ServiceConfig, _ string) {
			ref, _ := structpb.NewStruct(map[string]any{"$ref": "missing.yaml"})
			s.AdditionalProperties.Fields["metadata"] = structpb.NewStructValue(ref)
		}, code: exterrors.CodeInvalidServiceConfig},
		{name: "invalid image credentials hidden", change: func(s *azdext.ServiceConfig, _ string) {
			s.Image = "https://private-user:private-password@registry.example.com/agent?sig=private-signature#private-fragment"
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
			require.True(t, proto.Equal(before, service), "preview must not mutate the caller's service")
			if tc.code != "" {
				require.Error(t, err)
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, tc.code, local.Code)
				require.NotContains(t, err.Error(), "private-")
				return
			}
			require.NoError(t, err)
			require.Equal(t, "example-agent", definition.Name)
			require.NotSame(t, service, resolved)
			require.Nil(t, resolved.Config)
		})
	}
}

func TestPreviewRejectsEffectiveOverride(t *testing.T) {
	t.Setenv("AGENT_DEFINITION_PATH", "not-even-read.yaml")
	_, _, err := resolvePreviewDefinition(previewService(t), t.TempDir())
	require.ErrorContains(t, err, "AGENT_DEFINITION_PATH")
	require.ErrorContains(t, err, "unified")
}

func TestPreviewWholeDefinitionReferenceRegardlessOfFilename(t *testing.T) {
	t.Setenv("AGENT_DEFINITION_PATH", "")
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "custom-hosted-definition.yaml"),
		[]byte("kind: hosted\nname: file-agent\n"), 0600))
	service := previewService(t)
	var err error
	service.AdditionalProperties, err = structpb.NewStruct(map[string]any{"$ref": "custom-hosted-definition.yaml"})
	require.NoError(t, err)

	_, _, err = resolvePreviewDefinition(service, root)
	require.ErrorContains(t, err, "whole agent-file references")

	// The restriction belongs to preview, not the ordinary deployment loader.
	definition, hosted, _, err := LoadHostedAgentDefinition(service, root)
	require.NoError(t, err)
	require.True(t, hosted)
	require.Equal(t, "file-agent", definition.Name)

	service.AdditionalProperties = nil
	require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yml"),
		[]byte("kind: hosted\nname: legacy-file-agent\nimage: registry.example.com/agent:v1\n"), 0600))
	_, _, _, err = LoadHostedAgentDefinition(service, root)
	require.ErrorContains(t, err, "no runtime definition in azure.yaml")
}

func TestPreviewRequestDeployParity(t *testing.T) {
	for _, mode := range []string{"prebuilt", "build", "code", "legacy image marker"} {
		t.Run(mode, func(t *testing.T) {
			svc := previewService(t)
			env := map[string]string{"MODEL": "model-deployment"}
			svc.Environment = map[string]string{"FORWARDED": "$literal", "AZURE_AI_MODEL_DEPLOYMENT_NAME": env["MODEL"]}
			props := svc.AdditionalProperties.Fields
			props["description"] = structpb.NewStringValue("description")
			props["metadata"], _ = structpb.NewValue(map[string]any{"owner": "team", "authors": []any{"developer"}})
			props["environmentVariables"], _ = structpb.NewValue([]any{
				map[string]any{"name": "LEGACY", "value": "${MODEL}"},
				map[string]any{"name": "FORWARDED", "value": "overridden"},
			})
			props["container"], _ = structpb.NewValue(map[string]any{
				"resources": map[string]any{"cpu": "2", "memory": "4Gi"},
			})
			props["protocols"], _ = structpb.NewValue([]any{
				map[string]any{"protocol": "responses", "version": "2.0.0"},
				map[string]any{"protocol": "a2a", "version": "1.0.0"},
			})
			switch mode {
			case "build":
				svc.Image = ""
				svc.Docker.ImagePassthrough = false
			case "code":
				props["codeConfiguration"], _ = structpb.NewValue(map[string]any{
					"runtime": "python_3_13", "entryPoint": "app.py",
				})
			case "legacy image marker":
				svc.Docker.ImagePassthrough = false
				env["AZD_AGENT_SKIP_ACR"] = "true"
			}
			definition, _, _, _, err := AgentDefinitionFromService(svc)
			require.NoError(t, err)
			request, unknown, err := preparePreviewRequest(svc, definition, env)
			require.NoError(t, err)
			options := []agent_yaml.AgentBuildOption{}
			if mode == "build" {
				options = append(options, agent_yaml.WithImageURL("preview.invalid/unknown"))
			}
			deploy, err := prepareDeployRequest(svc, definition, env, options)
			require.NoError(t, err)
			require.Equal(t, deploy.request, request)
			hosted, ok := request.Definition.(agent_api.HostedAgentDefinition)
			require.True(t, ok)
			require.Equal(t, "model-deployment", hosted.EnvironmentVariables["LEGACY"])
			require.Equal(t, "$literal", hosted.EnvironmentVariables["FORWARDED"])
			require.Equal(t, "2", hosted.CPU)
			require.Equal(t, "4Gi", hosted.Memory)
			require.Equal(t, "team", request.Metadata["owner"])
			require.Equal(t, "true", request.Metadata["enableVnextExperience"])
			require.Len(t, hosted.ProtocolVersions, 2)
			if mode == "build" || mode == "code" {
				require.Len(t, unknown, 1)
			} else {
				require.Empty(t, unknown)
			}
		})
	}
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

func previewRequest(t *testing.T) *agent_api.CreateAgentRequest {
	t.Helper()
	svc := previewService(t)
	definition, _, _, _, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	request, _, err := preparePreviewRequest(svc, definition, nil)
	require.NoError(t, err)
	return request
}

func remotePreviewAgent(request *agent_api.CreateAgentRequest) *agent_api.AgentObject {
	agent := &agent_api.AgentObject{Name: request.Name, AgentEndpoint: request.AgentEndpoint, AgentCard: request.AgentCard}
	agent.Versions.Latest = agent_api.AgentVersionObject{
		Version: "1", Name: request.Name, Definition: request.Definition,
		Description: request.Description, Metadata: request.Metadata,
	}
	return agent
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
		{name: "unknown build", agent: remotePreviewAgent(request), status: "unknown",
			unknown: []string{"definition.container_configuration.image"}},
		{name: "unknown code", agent: remotePreviewAgent(request), status: "unknown", unknown: []string{"code"}},
		{name: "unauthorized", err: &azcore.ResponseError{StatusCode: http.StatusUnauthorized}},
		{name: "forbidden", err: &azcore.ResponseError{StatusCode: http.StatusForbidden}},
		{name: "credential-bearing service error", err: &azcore.ResponseError{
			StatusCode: http.StatusForbidden,
			ErrorCode:  "https://private-user:private-password@host?sig=private-sig#private-fragment",
		}},
		{name: "network", err: fmt.Errorf("https://user:password@host?sig=secret#fragment")},
		{name: "nil success"},
		{name: "empty success", agent: &agent_api.AgentObject{}},
		{name: "missing latest with valid name", agent: &agent_api.AgentObject{Name: request.Name}},
		{name: "latest without definition", agent: remotePreviewAgent(&agent_api.CreateAgentRequest{Name: request.Name})},
		{name: "invalid definition", agent: remotePreviewAgent(&agent_api.CreateAgentRequest{
			Name: "example-agent", CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{Definition: "malformed"},
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
				require.NotContains(t, err.Error(), "password")
				require.NotContains(t, err.Error(), "sig=")
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

func TestPreviewEffectiveChangesAndNonDisclosure(t *testing.T) {
	request := previewRequest(t)
	remote := remotePreviewAgent(request)
	hosted := request.Definition.(agent_api.HostedAgentDefinition)
	hosted.EnvironmentVariables = map[string]string{
		"API_KEY": "local-secret", "AZURE_AI_MODEL_DEPLOYMENT_NAME": "model-two",
	}
	hosted.CPU = "2"
	request.Definition = hosted
	request.Description = new("https://private-user:private-password@host/path?sig=private-sig#private-fragment")
	request.Metadata = map[string]string{"owner": "local-secret"}
	result, err := previewAgentRequest(t.Context(), &recordingPreviewReader{agent: remote}, "agent", request, nil)
	require.NoError(t, err)
	require.Equal(t, "update", result.Data.AsMap()["status"])
	require.Contains(t, result.Message, "definition.environment_variables.API_KEY")
	require.Contains(t, result.Message, "definition.environment_variables.AZURE_AI_MODEL_DEPLOYMENT_NAME")
	require.Contains(t, result.Message, "definition.cpu")
	for _, group := range []string{"metadata", "resources", "environmentVariables", "modelDeployment"} {
		require.Contains(t, result.Message, "  "+group+":\n")
	}
	var parsed agentPreviewResult
	encoded, err := json.Marshal(result.Data.AsMap())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &parsed))
	require.NotEmpty(t, parsed.Changes)
	for _, change := range parsed.Changes {
		require.Equal(t, previewFieldGroup(change.Path), change.Group)
	}
	for _, secret := range []string{"local-secret", "model-two", "private-user", "private-password", "private-sig",
		"private-fragment", "sig="} {
		require.NotContains(t, result.Message, secret)
		require.NotContains(t, string(encoded), secret)
	}
}

func TestPreviewProtocolOrderAndCPUFormatting(t *testing.T) {
	request := previewRequest(t)
	hosted := request.Definition.(agent_api.HostedAgentDefinition)
	hosted.CPU = "1.0"
	hosted.ProtocolVersions = []agent_api.ProtocolVersionRecord{
		{Protocol: "responses", Version: "2.0.0"}, {Protocol: "a2a", Version: "1.0.0"},
	}
	request.Definition = hosted
	remote := remotePreviewAgent(request)
	hosted.CPU = "1"
	hosted.ProtocolVersions = []agent_api.ProtocolVersionRecord{
		{Protocol: "a2a", Version: "1.0.0"}, {Protocol: "responses", Version: "2.0.0"},
		{Protocol: "responses", Version: "2.0.0"},
	}
	hosted.Image = hosted.ContainerConfiguration.Image
	remote.Versions.Latest.Definition = hosted
	result, err := comparePreviewRequest("agent", request, remote, nil)
	require.NoError(t, err)
	require.Equal(t, "noChange", result.Data.AsMap()["status"])
}

func TestPreviewURLAndWriter(t *testing.T) {
	//nolint:gosec // Deliberately fake credentials verify non-disclosure.
	url := "https://private-user:private-password@host/path?sig=private-sig#private-fragment"
	endpoint, err := previewProjectEndpoint(url)
	require.NoError(t, err)
	require.Equal(t, "https://host/path", endpoint)
	require.Equal(t, "at https://host/path", redactPreviewURLs("at "+url))
	for _, invalid := range []string{
		url + "%invalid", "http://private-user:private-password@host?sig=private-sig#private-fragment",
	} {
		_, err := previewProjectEndpoint(invalid)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private-")
		require.NotContains(t, err.Error(), "sig=")
	}
	var buffer bytes.Buffer
	require.NoError(t, writeAgentPreview(&buffer, agentPreviewResult{Status: "create"}))
	require.Contains(t, buffer.String(), "Would create")
	require.ErrorIs(t, writeAgentPreview(failingPreviewWriter{}, agentPreviewResult{}), errPreviewWrite)
}

var errPreviewWrite = errors.New("writer failed")

type failingPreviewWriter struct{}

func (failingPreviewWriter) Write([]byte) (int, error) { return 0, errPreviewWrite }

func TestPreviewUnknownEnvironment(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(`
name: preview
services:
  agent:
    env:
      MODEL: ${PREVIEW_MISSING_MODEL}
      PARTIAL: prefix-${PREVIEW_MISSING_MODEL}
      DEFAULT: ${PREVIEW_MISSING_DEFAULT:-fallback}
      EMPTY_DEFAULT: ${PREVIEW_MISSING_DEFAULT:-}
      LITERAL: ""
`), 0600))
	svc := previewService(t)
	svc.Environment = map[string]string{"MODEL": "", "DEFAULT": "fallback", "LITERAL": ""}
	pending, err := previewPendingEnvironment(root, svc, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		"definition.environment_variables.MODEL", "definition.environment_variables.PARTIAL",
	}, pending)
}

func TestPreviewInlineEnvironmentInputs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expression string
		override   bool
		unknown    bool
		invalid    bool
	}{
		{name: "required", expression: "${MISSING}", unknown: true},
		{name: "partial", expression: "prefix-${MISSING}", unknown: true},
		{name: "default", expression: "${MISSING:-fallback}"},
		{name: "empty default", expression: "${MISSING:-}"},
		{name: "server template", expression: "${{connections.example.credentials.key}}"},
		{name: "malformed", expression: "${MISSING", invalid: true},
		{name: "modern override", expression: "${MISSING", override: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := previewService(t)
			definition, _, _, _, err := AgentDefinitionFromService(service)
			require.NoError(t, err)
			definition.EnvironmentVariables = &[]agent_yaml.EnvironmentVariable{
				{Name: "VALUE", Value: tc.expression},
			}
			if tc.override {
				service.Environment = map[string]string{"VALUE": "literal"}
			}
			request, unknown, err := preparePreviewRequest(service, definition, nil)
			if tc.invalid {
				require.Error(t, err)
				require.Nil(t, request)
				require.NotContains(t, err.Error(), tc.expression)
				return
			}
			require.NoError(t, err)
			if tc.unknown {
				require.Equal(t, []string{"definition.environment_variables.VALUE"}, unknown)
			} else {
				require.Empty(t, unknown)
			}
		})
	}
}

func TestPreviewCodeReadNormalization(t *testing.T) {
	service := previewService(t)
	service.AdditionalProperties.Fields["codeConfiguration"], _ = structpb.NewValue(map[string]any{
		"runtime": "python_3_13", "entryPoint": "app.py",
	})
	definition, _, _, _, err := AgentDefinitionFromService(service)
	require.NoError(t, err)
	request, unknown, err := preparePreviewRequest(service, definition, nil)
	require.NoError(t, err)
	remote := remotePreviewAgent(request)
	hosted, ok := remote.Versions.Latest.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	code := *hosted.CodeConfiguration
	code.DependencyResolution = ""
	hosted.CodeConfiguration = &code
	hosted.ContainerConfiguration = &agent_api.ContainerConfigurationAPI{Image: "registry.example.com/platform-built:v1"}
	hosted.Image = hosted.ContainerConfiguration.Image
	remote.Versions.Latest.Definition = hosted
	result, err := comparePreviewRequest("agent", request, remote, unknown)
	require.NoError(t, err)
	require.Equal(t, "unknown", result.Data.AsMap()["status"])
	require.Empty(t, result.Data.AsMap()["changes"])
	require.Equal(t, "remote_build", request.Definition.(agent_api.HostedAgentDefinition).CodeConfiguration.DependencyResolution)
}

func TestPreviewArtifactSelection(t *testing.T) {
	for _, tc := range []struct {
		name        string
		passthrough bool
		image       string
		code        bool
		unknown     string
	}{
		{name: "explicit passthrough", image: "registry.example.com/agent:v1", passthrough: true},
		{name: "build output unknown", unknown: "definition.container_configuration.image"},
		{name: "image is not necessarily final", image: "registry.example.com/agent:v1",
			unknown: "definition.container_configuration.image"},
		{name: "code wins over image", image: "registry.example.com/agent:v1", passthrough: true, code: true, unknown: "code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := previewService(t)
			svc.Image = tc.image
			svc.Docker.ImagePassthrough = tc.passthrough
			definition, _, _, _, err := AgentDefinitionFromService(svc)
			require.NoError(t, err)
			if tc.code {
				definition.CodeConfiguration = &agent_yaml.CodeConfiguration{Runtime: "python_3_13", EntryPoint: "app.py"}
			}
			request, unknown, err := preparePreviewRequest(svc, definition, nil)
			require.NoError(t, err)
			if tc.unknown == "" {
				require.Empty(t, unknown)
			} else {
				require.Equal(t, []string{tc.unknown}, unknown)
			}
			remote := remotePreviewAgent(request)
			if tc.unknown == "definition.container_configuration.image" {
				hosted := remote.Versions.Latest.Definition.(agent_api.HostedAgentDefinition)
				hosted.ContainerConfiguration = &agent_api.ContainerConfigurationAPI{Image: "registry.example.com/built:v9"}
				remote.Versions.Latest.Definition = hosted
			}
			result, err := comparePreviewRequest("agent", request, remote, unknown)
			require.NoError(t, err)
			require.NotContains(t, result.Message, "preview.invalid")
			require.False(t, strings.Contains(result.Message, "No deployment") && len(unknown) > 0)
			if tc.unknown != "" {
				require.Equal(t, "unknown", result.Data.AsMap()["status"])
			}
		})
	}
}

func TestPreviewPrebuiltImageChange(t *testing.T) {
	request := previewRequest(t)
	remote := remotePreviewAgent(request)
	hosted := request.Definition.(agent_api.HostedAgentDefinition)
	hosted.ContainerConfiguration = &agent_api.ContainerConfigurationAPI{Image: "registry.example.com/agent:v2"}
	request.Definition = hosted
	result, err := comparePreviewRequest("agent", request, remote, nil)
	require.NoError(t, err)
	require.Equal(t, "update", result.Data.AsMap()["status"])
	require.Contains(t, result.Message, "update: definition.container_configuration.image")
}

func TestPreviewDefaultsAndUnpatchedEndpointFields(t *testing.T) {
	request := previewRequest(t)
	request.AgentEndpoint = &agent_api.AgentEndpoint{Protocols: []agent_api.AgentEndpointProtocol{"responses"}}
	remote := remotePreviewAgent(request)
	remote.AgentEndpoint = &agent_api.AgentEndpoint{
		Protocols: []agent_api.AgentEndpointProtocol{"responses"},
		VersionSelector: &agent_api.VersionSelector{VersionSelectionRules: []agent_api.VersionSelectionRule{
			{Type: agent_api.VersionSelectorTypeFixedRatio, AgentVersion: "1", TrafficPercentage: new(int32(100))},
		}},
	}
	hosted := request.Definition.(agent_api.HostedAgentDefinition)
	hosted.SessionConfiguration = &agent_api.SessionConfigurationAPI{IdleTimeoutSeconds: 900}
	remote.Versions.Latest.Definition = hosted
	result, err := comparePreviewRequest("agent", request, remote, nil)
	require.NoError(t, err)
	require.Equal(t, "noChange", result.Data.AsMap()["status"])
}

func TestPreviewScenarioContract(t *testing.T) {
	root := filepath.Join("..", "..", "tests", "cli-interactive-tester-scenarios")
	data, err := os.ReadFile(filepath.Join(root, "tier2", "2.17a-deploy-preview.yaml"))
	require.NoError(t, err)
	var scenario struct {
		Name     string   `yaml:"name"`
		Command  string   `yaml:"command"`
		Cwd      string   `yaml:"cwd"`
		Tags     []string `yaml:"tags"`
		Requires string   `yaml:"requires"`
		Goals    []string `yaml:"goals"`
	}
	require.NoError(t, yaml.Unmarshal(data, &scenario))
	require.NotEmpty(t, scenario.Name)
	require.Contains(t, scenario.Command, "azd deploy --preview")
	require.NotEmpty(t, scenario.Cwd)
	require.Equal(t, []string{"tier:2", "cmd:deploy", "serial-only"}, scenario.Tags)
	require.FileExists(t, filepath.Join(root, filepath.FromSlash(scenario.Requires)))
	require.NotEmpty(t, scenario.Goals)
	for _, match := range regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`).FindAllStringSubmatch(string(data), -1) {
		require.Equal(t, "shared_agent_name", match[1], "unknown scenario placeholder")
	}
}
