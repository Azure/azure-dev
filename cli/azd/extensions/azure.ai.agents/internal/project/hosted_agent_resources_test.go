// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestHostedAgentResourcesRejectInvalidStructure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		container any
		wantError string
	}{
		{"container null", nil, "object"},
		{"container scalar", "4", "object"},
		{"container list", []any{}, "object"},
		{"unknown container field", map[string]any{"resource": map[string]any{}}, "resource"},
		{"resources null", map[string]any{"resources": nil}, "object"},
		{"resources scalar", map[string]any{"resources": "4"}, "object"},
		{"resources list", map[string]any{"resources": []any{}}, "object"},
		{"unknown cpu field", map[string]any{"resources": map[string]any{"core": "4", "memory": "1Gi"}}, "core"},
		{"unknown memory field", map[string]any{"resources": map[string]any{"cpu": "1", "memroy": "2Gi"}}, "memroy"},
		{"numeric cpu", map[string]any{"resources": map[string]any{"cpu": 4}}, "string"},
		{"boolean cpu", map[string]any{"resources": map[string]any{"cpu": true}}, "string"},
		{"null cpu", map[string]any{"resources": map[string]any{"cpu": nil}}, "string"},
		{"empty cpu", map[string]any{"resources": map[string]any{"cpu": ""}}, "pattern"},
		{"blank cpu", map[string]any{"resources": map[string]any{"cpu": " "}}, "pattern"},
		{"invalid cpu", map[string]any{"resources": map[string]any{"cpu": "4core"}}, "pattern"},
		{"numeric memory", map[string]any{"resources": map[string]any{"memory": 8}}, "string"},
		{"null memory", map[string]any{"resources": map[string]any{"memory": nil}}, "string"},
		{"empty memory", map[string]any{"resources": map[string]any{"memory": ""}}, "pattern"},
		{"invalid memory", map[string]any{"resources": map[string]any{"memory": "8GiB"}}, "pattern"},
	}
	for _, test := range tests {
		for _, source := range []string{"inline", "root-ref"} {
			for _, mode := range []string{"container", "code"} {
				t.Run(test.name+"/"+source+"/"+mode, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					service := resourceValidationService(t, root, source, mode, test.container)
					original := proto.CloneOf(service)
					provider := &AgentServiceTargetProvider{projectPath: root}
					if source == "inline" {
						_, err := LoadServiceTargetAgentConfig(service)
						require.ErrorContains(t, err, test.wantError)
						require.True(t, proto.Equal(original, service))
						data, err := json.Marshal(service.AdditionalProperties.AsMap())
						require.NoError(t, err)
						filePath := filepath.Join(root, "direct.json")
						require.NoError(t, os.WriteFile(filePath, data, 0o600))

						_, err = LoadAgentDefinitionFile(root, "direct.json", service.Name)

						require.ErrorContains(t, err, test.wantError)
						after, err := os.ReadFile(filePath)
						require.NoError(t, err)
						require.Equal(t, data, after)
					}

					_, err := provider.Package(t.Context(), service, nil, func(string) {})

					localErr, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok, "expected resource validation, got %v", err)
					require.Equal(t, exterrors.CodeInvalidServiceConfig, localErr.Code)
					require.Contains(t, localErr.Message, "agent-service")
					require.Contains(t, localErr.Message, "container")
					require.Contains(t, localErr.Message, test.wantError)
					require.Contains(t, localErr.Suggestion, "container.resources")
					require.Nil(t, provider.credential)
					require.Nil(t, provider.env)

					_, err = ValidateAgentServiceDefinition(original, root)
					require.ErrorContains(t, err, test.wantError)
				})
			}
		}
	}
}

func TestHostedAgentResourcesPreserveDefaultsAndServiceValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		container any
		cpu       string
		memory    string
	}{
		{"empty container", map[string]any{}, DefaultCpu, DefaultMemory},
		{"empty resources", map[string]any{"resources": map[string]any{}}, DefaultCpu, DefaultMemory},
		{"cpu only", map[string]any{"resources": map[string]any{"cpu": "4"}}, "4", DefaultMemory},
		{"memory only", map[string]any{"resources": map[string]any{"memory": "8Gi"}}, DefaultCpu, "8Gi"},
		{"four cores", map[string]any{"resources": map[string]any{"cpu": "4.0", "memory": "8.0Gi"}}, "4.0", "8.0Gi"},
		{"new tier", map[string]any{"resources": map[string]any{"cpu": "8", "memory": "16Gi"}}, "8", "16Gi"},
		{"server rejects pair", map[string]any{"resources": map[string]any{"cpu": "4", "memory": "4Gi"}}, "4", "4Gi"},
		{"schema units", map[string]any{"resources": map[string]any{"cpu": "500m", "memory": "512Mi"}}, "500m", "512Mi"},
	}
	for _, test := range tests {
		for _, source := range []string{"inline", "root-ref", "container-ref", "resources-ref"} {
			for _, mode := range []string{"container", "code"} {
				t.Run(test.name+"/"+source+"/"+mode, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					service := resourceValidationService(t, root, source, mode, test.container)
					provider := &AgentServiceTargetProvider{projectPath: root}
					require.NoError(t, provider.Initialize(t.Context(), service))
					require.NoError(t, ResolveAgentContainerSettings(service, root))
					_, err := ValidateAgentServiceDefinition(service, root)
					require.NoError(t, err)
					definition, _, found, _, err := AgentDefinitionFromService(service)
					require.NoError(t, err)
					require.True(t, found)
					before := proto.CloneOf(service)

					request, err := prepareDeployRequest(service, definition, nil, nil)

					require.NoError(t, err)
					hosted, ok := request.request.Definition.(agent_api.HostedAgentDefinition)
					require.True(t, ok)
					require.Equal(t, test.cpu, hosted.CPU)
					require.Equal(t, test.memory, hosted.Memory)
					require.True(t, proto.Equal(before, service), "request preparation must not mutate config")
				})
			}
		}
	}
}

func TestHostedAgentResourcesRejectUnknownFieldsInFragments(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"container-ref", "resources-ref", "cpu-ref", "memory-ref"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			service := resourceValidationService(t, root, source, "container",
				map[string]any{"resources": map[string]any{"core": "4", "memory": "8Gi"}})
			provider := &AgentServiceTargetProvider{azdClient: newInitializeTestClient(t, root)}

			_, err := provider.Package(t.Context(), service, nil, func(string) {})

			require.Error(t, err)
			require.Contains(t, err.Error(), "agent-service")
			require.Nil(t, provider.credential)
			require.Nil(t, provider.env)
		})
	}
}

func TestHostedAgentResourcesValidateDeploymentEntrypoints(t *testing.T) {
	t.Parallel()
	for _, entrypoint := range []string{"target", "package", "publish", "deploy"} {
		t.Run(entrypoint, func(t *testing.T) {
			t.Parallel()
			service := resourceValidationService(t, t.TempDir(), "inline", "container",
				map[string]any{"resources": map[string]any{"core": "4"}})
			provider := &AgentServiceTargetProvider{}
			require.NoError(t, provider.Initialize(t.Context(), service))
			var err error
			switch entrypoint {
			case "target":
				_, err = provider.GetTargetResource(t.Context(), "", service, nil)
			case "package":
				_, err = provider.Package(t.Context(), service, nil, func(string) {})
			case "publish":
				_, err = provider.Publish(t.Context(), service, nil, nil, nil, func(string) {})
			case "deploy":
				_, err = provider.Deploy(t.Context(), service, nil, nil, func(string) {})
			}
			require.ErrorContains(t, err, "core")
			require.Nil(t, provider.credential)
			require.Nil(t, provider.env)
		})
	}
}

func TestHostedAgentResourcesDoNotDiscloseInvalidValues(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"cpu", "memory", "$ref"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			// #nosec G101 -- Synthetic URL verifies that credentials are never disclosed.
			secretURL := "https://private-user:private-password@host.invalid/path?sig=private-signature#private-fragment"
			resources := map[string]any{field: secretURL}
			service := resourceValidationService(t, t.TempDir(), "inline", "container",
				map[string]any{"resources": resources})
			err := ResolveAgentContainerSettings(service, t.TempDir())

			require.Error(t, err)
			require.Contains(t, err.Error(), "agent-service")
			require.NotContains(t, err.Error(), "private-")
			require.NotContains(t, err.Error(), "sig=")
		})
	}
}

func TestHostedAgentResourcesMissingContainerUsesDefaults(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"inline", "root-ref"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			service := validationTestService(t, root, source, map[string]any{
				"kind": "hosted", "name": "resource-agent",
			})
			service.Image = "registry.example.com/agent:v1"
			provider := &AgentServiceTargetProvider{projectPath: root}
			require.NoError(t, provider.Initialize(t.Context(), service))
			definition, _, _, _, err := AgentDefinitionFromService(service)
			require.NoError(t, err)

			request, err := prepareDeployRequest(service, definition, nil, nil)

			require.NoError(t, err)
			hosted, ok := request.request.Definition.(agent_api.HostedAgentDefinition)
			require.True(t, ok)
			require.Equal(t, DefaultCpu, hosted.CPU)
			require.Equal(t, DefaultMemory, hosted.Memory)
		})
	}
}

func resourceValidationService(
	t *testing.T, root, source, mode string, container any,
) *azdext.ServiceConfig {
	t.Helper()
	values := map[string]any{
		"kind": "hosted", "name": "resource-agent", "container": container,
		"futureProperty": map[string]any{"retained": true},
		"metadata":       map[string]any{"tags": []any{"resource-validation"}},
	}
	if mode == "code" {
		values["codeConfiguration"] = map[string]any{"runtime": "python_3_13", "entryPoint": "app.py"}
	}
	if source == "container-ref" || source == "resources-ref" || source == "cpu-ref" || source == "memory-ref" {
		document := container
		if source == "cpu-ref" || source == "memory-ref" {
			document = map[string]any{"invalid": true}
			field := "cpu"
			if source == "memory-ref" {
				field = "memory"
			}
			values["container"] = map[string]any{
				"resources": map[string]any{field: map[string]any{"$ref": "./resources.json"}},
			}
		} else if source == "resources-ref" {
			if object, ok := container.(map[string]any); ok {
				if resources, found := object["resources"]; found {
					document = resources
					values["container"] = map[string]any{"resources": map[string]any{"$ref": "./resources.json"}}
				} else {
					values["container"] = map[string]any{"$ref": "./resources.json"}
				}
			} else {
				values["container"] = map[string]any{"$ref": "./resources.json"}
			}
		} else {
			values["container"] = map[string]any{"$ref": "./resources.json"}
		}
		data, err := json.Marshal(document)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, "resources.json"), data, 0o600))
	}
	sourceKind := "inline"
	if source == "root-ref" {
		sourceKind = source
	}
	service := validationTestService(t, root, sourceKind, values)
	service.Environment = map[string]string{"FOO": "bar"}
	service.Image = "registry.example.com/agent:v1"
	if mode == "code" {
		service.Image = ""
	}
	return service
}
