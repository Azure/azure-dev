// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func deploymentPreviewRequest() *agent_api.CreateAgentRequest {
	return &agent_api.CreateAgentRequest{
		Name: "research-agent",
		CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
			Description: new("Research assistant"),
			Metadata:    map[string]string{"owner": "research", "enableVnextExperience": "true"},
			Definition: agent_api.HostedAgentDefinition{
				AgentDefinition: agent_api.AgentDefinition{Kind: agent_api.AgentKindHosted},
				ProtocolVersions: []agent_api.ProtocolVersionRecord{
					{Protocol: agent_api.AgentProtocolResponses, Version: "2.0.0"},
					{Protocol: agent_api.AgentProtocolActivityProtocol, Version: "1.0.0"},
				},
				CPU:    DefaultCpu,
				Memory: DefaultMemory,
				EnvironmentVariables: map[string]string{
					"KEEP": "same", "REMOVE": "removed-secret", "CHANGE": "old-secret",
					"AZURE_AI_MODEL_DEPLOYMENT_NAME": "old-model",
				},
				CodeConfiguration: &agent_api.CodeConfigurationAPI{
					Runtime: "python_3_13", EntryPoint: []string{"python", "main.py"}, DependencyResolution: "remote_build",
				},
			},
		},
	}
}

func deployedPreviewAgent(t *testing.T, request *agent_api.CreateAgentRequest) *agent_api.AgentObject {
	t.Helper()
	data, err := json.Marshal(request)
	require.NoError(t, err)
	var latest agent_api.AgentVersionObject
	require.NoError(t, json.Unmarshal(data, &latest))
	latest.Version = "7"
	latest.Status = "active"
	latest.ID = "server-generated-id"
	latest.CreatedAt = 123456
	agent := &agent_api.AgentObject{Name: request.Name, AgentEndpoint: request.AgentEndpoint, AgentCard: request.AgentCard}
	agent.Versions.Latest = latest
	return agent
}

func TestCompareAgentDeploymentGroupsChanges(t *testing.T) {
	t.Parallel()
	request := deploymentPreviewRequest()
	existing := deployedPreviewAgent(t, request)
	definition, ok := request.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)

	request.Description = new("Updated assistant")
	request.Metadata["owner"] = "engineering"
	request.Metadata["tags"] = `["Test"]`
	definition.ProtocolVersions = []agent_api.ProtocolVersionRecord{
		{Protocol: agent_api.AgentProtocolResponses, Version: "3.0.0"},
	}
	definition.CPU = "1"
	definition.Memory = "2Gi"
	delete(definition.EnvironmentVariables, "REMOVE")
	definition.EnvironmentVariables["ADD"] = "added-secret"
	definition.EnvironmentVariables["CHANGE"] = "new-secret"
	definition.EnvironmentVariables["AZURE_AI_MODEL_DEPLOYMENT_NAME"] = "new-model"
	definition.CodeConfiguration.EntryPoint = []string{"python", "app.py"}
	definition.SessionConfiguration = &agent_api.SessionConfigurationAPI{IdleTimeoutSeconds: 600}
	definition.RaiConfig = &agent_api.RaiConfig{RaiPolicyName: "safety-policy"}
	request.Definition = definition

	result, err := compareAgentDeployment(request, existing, nil)
	require.NoError(t, err)
	assert.Equal(t, "create_version", result.Operation)
	assert.Equal(t, "7", result.CurrentVersion)
	assert.True(t, result.HasChanges)
	require.Equal(t, []DeployPreviewChangeGroup{
		{Group: "metadata", Changes: []DeployPreviewChange{
			{Field: "description", Kind: "modify", Before: "Research assistant", After: "Updated assistant"},
			{Field: "metadata.owner", Kind: "modify", Before: "research", After: "engineering"},
			{Field: "metadata.tags", Kind: "add", After: []any{"Test"}},
		}},
		{Group: "protocols", Changes: []DeployPreviewChange{
			{Field: "protocols", Kind: "modify",
				Before: []any{
					map[string]any{"protocol": "activity", "version": "1.0.0"},
					map[string]any{"protocol": "responses", "version": "2.0.0"},
				},
				After: []any{map[string]any{"protocol": "responses", "version": "3.0.0"}}},
		}},
		{Group: "resources", Changes: []DeployPreviewChange{
			{Field: "cpu", Kind: "modify", Before: DefaultCpu, After: "1"},
			{Field: "memory", Kind: "modify", Before: DefaultMemory, After: "2Gi"},
		}},
		{Group: "environmentVariables", Changes: []DeployPreviewChange{
			{Field: "ADD", Kind: "add", After: "[redacted]", Sensitive: true},
			{Field: "CHANGE", Kind: "modify", Before: "[redacted]", After: "[redacted]", Sensitive: true},
			{Field: "REMOVE", Kind: "remove", Before: "[redacted]", Sensitive: true},
		}},
		{Group: "modelDeployment", Changes: []DeployPreviewChange{
			{Field: "AZURE_AI_MODEL_DEPLOYMENT_NAME", Kind: "modify", Before: "old-model", After: "new-model"},
		}},
		{Group: "code", Changes: []DeployPreviewChange{
			{Field: "entry_point", Kind: "modify", Before: []any{"python", "main.py"}, After: []any{"python", "app.py"}},
		}},
		{Group: "session", Changes: []DeployPreviewChange{
			{Field: "idle_timeout_seconds", Kind: "modify", Before: float64(900), After: float64(600)},
		}},
		{Group: "contentSafety", Changes: []DeployPreviewChange{
			{Field: "rai_policy_name", Kind: "add", After: "safety-policy"},
		}},
	}, result.Changes)
}

func TestCompareAgentDeploymentNoChanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(map[string]any, *agent_api.AgentObject)
	}{
		{name: "identical"},
		{name: "protocol order and legacy spellings", change: func(definition map[string]any, _ *agent_api.AgentObject) {
			definition["protocol_versions"] = []any{
				map[string]any{"protocol": "activity_protocol", "version": "v1"},
				map[string]any{"protocol": "responses", "version": "2.0.0"},
			}
		}},
		{name: "legacy protocol property", change: func(definition map[string]any, _ *agent_api.AgentObject) {
			definition["container_protocol_versions"] = definition["protocol_versions"]
			delete(definition, "protocol_versions")
		}},
		{name: "equivalent CPU", change: func(definition map[string]any, _ *agent_api.AgentObject) {
			definition["cpu"] = "0.50"
		}},
		{name: "explicit session default", change: func(definition map[string]any, _ *agent_api.AgentObject) {
			definition["session_configuration"] = map[string]any{"idle_timeout_seconds": 900}
		}},
		{name: "service managed code image", change: func(definition map[string]any, _ *agent_api.AgentObject) {
			definition["container_configuration"] = map[string]any{"image": "registry.example.com/generated:v7"}
		}},
		{name: "preserve unconfigured endpoint and card", change: func(_ map[string]any, agent *agent_api.AgentObject) {
			agent.AgentEndpoint = &agent_api.AgentEndpoint{
				Protocols: []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolResponses},
			}
			agent.AgentCard = &agent_api.AgentCard{Description: "Existing card"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := deploymentPreviewRequest()
			existing := deployedPreviewAgent(t, request)
			definition, ok := existing.Versions.Latest.Definition.(map[string]any)
			require.True(t, ok)
			if tt.change != nil {
				tt.change(definition, existing)
			}
			before, err := json.Marshal(existing)
			require.NoError(t, err)

			result, err := compareAgentDeployment(request, existing, nil)
			require.NoError(t, err)
			assert.False(t, result.HasChanges)
			assert.Empty(t, result.Changes)
			assert.NotNil(t, result.Changes, "JSON must contain an empty array, not null")
			after, err := json.Marshal(existing)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "comparison must not mutate deployed state")
		})
	}
}

func TestCompareAgentDeploymentOptionalDefaults(t *testing.T) {
	t.Parallel()
	request := deploymentPreviewRequest()
	request.Description = nil
	definition, ok := request.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	definition.EnvironmentVariables = nil
	request.Definition = definition
	existing := deployedPreviewAgent(t, request)
	existing.Versions.Latest.Description = new("")
	remote, ok := existing.Versions.Latest.Definition.(map[string]any)
	require.True(t, ok)
	remote["environment_variables"] = map[string]any{}
	code, ok := remote["code_configuration"].(map[string]any)
	require.True(t, ok)
	delete(code, "dependency_resolution")

	result, err := compareAgentDeployment(request, existing, nil)
	require.NoError(t, err)
	assert.False(t, result.HasChanges)
	assert.Empty(t, result.Changes)
}

func TestCompareAgentDeploymentEndpointPatch(t *testing.T) {
	t.Parallel()
	request := deploymentPreviewRequest()
	existing := deployedPreviewAgent(t, request)
	existing.AgentEndpoint = &agent_api.AgentEndpoint{
		Protocols: []agent_api.AgentEndpointProtocol{
			agent_api.AgentEndpointProtocolResponses, agent_api.AgentEndpointProtocolActivity,
		},
		AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
			{Type: agent_api.AgentEndpointAuthSchemeEntra},
		},
	}
	request.AgentEndpoint = &agent_api.AgentEndpoint{
		Protocols: []agent_api.AgentEndpointProtocol{
			agent_api.AgentEndpointProtocolActivity, agent_api.AgentEndpointProtocolResponses,
		},
	}
	result, err := compareAgentDeployment(request, existing, nil)
	require.NoError(t, err)
	assert.False(t, result.HasChanges, "protocol order and omitted authorization must not cause changes")

	request.AgentEndpoint.Protocols = []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolResponses}
	result, err = compareAgentDeployment(request, existing, nil)
	require.NoError(t, err)
	require.Len(t, result.Changes, 1)
	assert.Equal(t, "endpoint", result.Changes[0].Group)
	require.Len(t, result.Changes[0].Changes, 1)
	assert.Equal(t, "protocols", result.Changes[0].Changes[0].Field)
}

func TestCompareAgentDeploymentPendingToolboxValues(t *testing.T) {
	t.Parallel()
	request := deploymentPreviewRequest()
	existing := deployedPreviewAgent(t, request)
	remote, ok := existing.Versions.Latest.Definition.(map[string]any)
	require.True(t, ok)
	environment, ok := remote["environment_variables"].(map[string]any)
	require.True(t, ok)
	environment["TOOLBOX_VERSION"] = "5"
	environment["TOOLBOX_ENDPOINT"] = "https://example.com/previous-toolbox?sig=old-secret"

	result, err := compareAgentDeployment(request, existing, []string{"TOOLBOX_VERSION", "TOOLBOX_ENDPOINT"})
	require.NoError(t, err)
	assert.True(t, result.HasChanges)
	require.Equal(t, []DeployPreviewChangeGroup{{
		Group: "environmentVariables",
		Changes: []DeployPreviewChange{
			{Field: "TOOLBOX_ENDPOINT", Kind: "pending", Before: "[redacted]", Sensitive: true},
			{Field: "TOOLBOX_VERSION", Kind: "pending", Before: "[redacted]", Sensitive: true},
		},
	}}, result.Changes)
}

func TestCompareAgentDeploymentRedactsCredentials(t *testing.T) {
	t.Parallel()
	request := deploymentPreviewRequest()
	request.Metadata["link"] = "See https://old-user:old-password@example.com/path?sig=old-token#old-fragment"
	existing := deployedPreviewAgent(t, request)
	request.Metadata["link"] = "See https://new-user:new-password@example.com/path?sig=new-token#new-fragment"
	request.Metadata["https://key-user:key-password@example.com/key?sig=key-token#key-fragment"] = "value"
	definition, ok := request.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	definition.EnvironmentVariables["CHANGE"] = "a-new-plain-secret"
	request.Definition = definition
	// #nosec G101 -- Synthetic URL credentials exercise preview redaction.
	request.AgentCard = &agent_api.AgentCard{
		Description: "https://card-user:card-password@example.com/card?sig=card-token#card-fragment",
		// #nosec G101 -- Synthetic URL credentials exercise redaction inside nested arrays.
		Skills: []agent_api.AgentCardSkill{{
			ID: "one", Name: "Skill",
			Description: "https://skill-user:skill-password@example.com/skill?sig=skill-token#skill-fragment",
		}},
	}

	result, err := compareAgentDeployment(request, existing, nil)
	require.NoError(t, err)
	require.True(t, result.HasChanges, "compare before redacting, even when safe values are identical")
	data, err := json.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{
		"old-user", "old-password", "old-token", "old-fragment",
		"new-user", "new-password", "new-token", "new-fragment",
		"card-user", "card-password", "card-token", "card-fragment",
		"skill-user", "skill-password", "skill-token", "skill-fragment",
		"key-user", "key-password", "key-token", "key-fragment",
		"a-new-plain-secret", "old-secret",
	} {
		assert.NotContains(t, string(data), secret)
	}
	assert.Contains(t, string(data), "https://example.com/path")
	assert.Contains(t, string(data), "https://example.com/card")
	assert.Contains(t, string(data), "https://example.com/skill")
}

func TestCompareAgentDeploymentContainerToCode(t *testing.T) {
	t.Parallel()
	request := deploymentPreviewRequest()
	existing := deployedPreviewAgent(t, request)
	remote, ok := existing.Versions.Latest.Definition.(map[string]any)
	require.True(t, ok)
	delete(remote, "code_configuration")
	remote["image"] = "registry.example.com/agent:old"

	result, err := compareAgentDeployment(request, existing, nil)
	require.NoError(t, err)
	assert.True(t, result.HasChanges)
	require.Len(t, result.Changes, 2)
	assert.Equal(t, DeployPreviewChangeGroup{
		Group: "containerImage",
		Changes: []DeployPreviewChange{{
			Field: "image", Kind: "remove", Before: "registry.example.com/agent:old",
		}},
	}, result.Changes[0])
	assert.Equal(t, "code", result.Changes[1].Group)
}

func TestRedactPreviewErrorPreservesClassification(t *testing.T) {
	t.Parallel()
	// #nosec G101 -- Synthetic URL credentials verify error-message redaction.
	const message = "GET https://user:password@example.com/path?sig=token#fragment failed"
	localErr := exterrors.Internal(exterrors.OpGetAgent, message)
	serviceErr := &azdext.ServiceError{
		Message: message, ErrorCode: "get_agent.403", StatusCode: http.StatusForbidden, ServiceName: "example.com",
	}
	for _, original := range []error{localErr, serviceErr} {
		redacted := redactPreviewError(original)
		assert.Contains(t, redacted.Error(), "https://example.com/path")
		for _, secret := range []string{"user", "password", "token", "fragment"} {
			assert.NotContains(t, redacted.Error(), secret)
		}
		assert.Contains(t, original.Error(), message, "redaction must not mutate the original error")
	}
	redacted, ok := errors.AsType[*azdext.ServiceError](redactPreviewError(serviceErr))
	require.True(t, ok)
	assert.Equal(t, serviceErr.ErrorCode, redacted.ErrorCode)
	assert.Equal(t, serviceErr.StatusCode, redacted.StatusCode)
	assert.Equal(t, serviceErr.ServiceName, redacted.ServiceName)
}

type previewAgentReader struct {
	result  *agent_api.AgentObject
	err     error
	calls   int
	name    string
	version string
	digital bool
}

func (r *previewAgentReader) GetAgent(
	_ context.Context, name, version string, digital bool,
) (*agent_api.AgentObject, error) {
	r.calls++
	r.name, r.version, r.digital = name, version, digital
	return r.result, r.err
}

func TestPreviewStandaloneHostedAgentReadOnly(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	codeDirectory := t.TempDir()
	path := filepath.Join(directory, "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: research-agent
kind: hosted
code_configuration:
  runtime: dotnet_9
  entry_point: Agent.dll
  dependency_resolution: bundled
`), 0o600))
	before, err := os.ReadDir(codeDirectory)
	require.NoError(t, err)
	reader := &previewAgentReader{err: &azcore.ResponseError{StatusCode: http.StatusNotFound}}
	result, err := previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
		DefinitionPath:  path,
		CodePath:        codeDirectory,
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project/",
		Progress:        func(string) { t.Fatal("dry-run must not package or build source") },
	}, nil, func(endpoint string) (standaloneAgentReader, error) {
		assert.Equal(t, "https://account.services.ai.azure.com/api/projects/project", endpoint)
		return reader, nil
	})
	require.NoError(t, err, "bundled .NET packaging would fail because there is no .NET project to build")
	assert.Equal(t, 1, reader.calls)
	assert.Equal(t, "research-agent", reader.name)
	assert.Equal(t, agent_api.AgentEndpointAPIVersion, reader.version)
	assert.True(t, reader.digital)
	assert.Equal(t, "create", result.Operation)
	assert.True(t, result.HasChanges)
	assert.Empty(t, result.CurrentVersion)
	assert.Equal(t, codeDirectory, result.SourcePath)
	assert.Contains(t, result.Notes, "Bundled .NET dependencies would be built locally during deployment.")
	after, err := os.ReadDir(codeDirectory)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestPreviewStandaloneHostedAgentReadErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result *agent_api.AgentObject
		err    error
		code   string
	}{
		{name: "unauthorized", err: &azcore.ResponseError{StatusCode: http.StatusUnauthorized}, code: "get_agent.401"},
		{name: "forbidden", err: &azcore.ResponseError{StatusCode: http.StatusForbidden}, code: "get_agent.403"},
		{name: "server error", err: &azcore.ResponseError{StatusCode: http.StatusInternalServerError}, code: "get_agent.500"},
		{name: "network error", err: errors.New("network unavailable")},
		{name: "cancelled", err: context.Canceled},
		{name: "nil response"},
		{name: "missing latest definition", result: &agent_api.AgentObject{Name: "research-agent"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "agent.yaml")
			require.NoError(t, os.WriteFile(path, []byte("name: research-agent\nkind: hosted\n"), 0o600))
			reader := &previewAgentReader{result: tt.result, err: tt.err}
			result, err := previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
				DefinitionPath: path, ProjectEndpoint: "https://example.com",
			}, nil, func(string) (standaloneAgentReader, error) { return reader, nil })
			require.Error(t, err)
			assert.Nil(t, result, "errors must not be reported as a create preview")
			assert.Equal(t, 1, reader.calls)
			if tt.code != "" {
				serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
				require.True(t, ok)
				assert.Equal(t, tt.code, serviceErr.ErrorCode)
			}
		})
	}
}

func TestPreviewStandaloneHostedAgentInputValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		yaml     string
		codePath string
		endpoint string
		code     string
	}{
		{name: "missing definition", code: exterrors.CodeAgentDefinitionNotFound},
		{name: "invalid YAML", yaml: "name: [", code: exterrors.CodeInvalidAgentManifest},
		{name: "invalid image", yaml: "name: agent\nkind: hosted\nimage: 'not a valid reference'\n",
			code: exterrors.CodeInvalidAgentManifest},
		{name: "missing code", yaml: "name: agent\nkind: hosted\n", codePath: "missing-source",
			code: exterrors.CodeInvalidFilePath},
		{name: "missing endpoint", yaml: "name: agent\nkind: hosted\n", endpoint: " ",
			code: exterrors.CodeMissingAiProjectEndpoint},
		{name: "invalid session", yaml: `name: agent
kind: hosted
session_configuration:
  idle_timeout_seconds: 1
`, code: exterrors.CodeInvalidAgentRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			path := filepath.Join(directory, "agent.yaml")
			if tt.yaml != "" {
				require.NoError(t, os.WriteFile(path, []byte(tt.yaml), 0o600))
			}
			codePath := ""
			if tt.codePath != "" {
				codePath = filepath.Join(directory, tt.codePath)
			}
			endpoint := tt.endpoint
			if endpoint == "" {
				endpoint = "https://example.com"
			}
			result, err := previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
				DefinitionPath: path, CodePath: codePath, ProjectEndpoint: endpoint,
			}, nil, func(string) (standaloneAgentReader, error) {
				t.Fatal("invalid inputs must fail before authentication or API access")
				return nil, nil
			})
			require.Error(t, err)
			assert.Nil(t, result)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, tt.code, localErr.Code)
		})
	}
}

func TestPreviewStandaloneHostedAgentNoChangesAndCredentialFailure(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte("name: research-agent\nkind: hosted\n"), 0o600))
	options := DirectDeployOptions{DefinitionPath: path, ProjectEndpoint: "https://example.com"}
	prepared, err := loadStandaloneHostedAgent(options)
	require.NoError(t, err)
	request, err := standaloneAgentRequest(prepared.definition, prepared.environment)
	require.NoError(t, err)
	reader := &previewAgentReader{result: deployedPreviewAgent(t, request)}

	result, err := previewStandaloneHostedAgent(t.Context(), options, nil,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.False(t, result.HasChanges)
	assert.Empty(t, result.Changes)
	assert.Equal(t, "create_version", result.Operation)
	assert.Equal(t, filepath.Dir(path), result.SourcePath)

	credentialErr := errors.New("credential unavailable")
	result, err = previewStandaloneHostedAgent(t.Context(), options, nil,
		func(string) (standaloneAgentReader, error) { return nil, credentialErr })
	assert.Nil(t, result)
	assert.ErrorIs(t, err, credentialErr)
}

func TestCompareAgentDeploymentRejectsInvalidRemoteDefinition(t *testing.T) {
	t.Parallel()
	for _, definition := range []any{nil, "not-an-object", map[string]any{"kind": "workflow"}} {
		t.Run(fmt.Sprintf("%T", definition), func(t *testing.T) {
			t.Parallel()
			request := deploymentPreviewRequest()
			existing := deployedPreviewAgent(t, request)
			existing.Versions.Latest.Definition = definition
			result, err := compareAgentDeployment(request, existing, nil)
			require.Error(t, err)
			assert.Nil(t, result)
		})
	}
}
