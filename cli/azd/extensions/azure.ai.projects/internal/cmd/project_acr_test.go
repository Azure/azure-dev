// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testExternalRegistryID = "/subscriptions/sub/resourceGroups/external/providers/" +
	"Microsoft.ContainerRegistry/registries/shared"

func TestProjectAddACRInvalidFlagsBeforeDaemon(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"empty", []string{"--new-project", "--acr-resource-id="}, "cannot be empty"},
		{"invalid ID", []string{"--new-project", "--acr-resource-id=shared"}, "full ARM ID"},
		{"query suffix", []string{"--new-project", "--acr-resource-id=" +
			testExternalRegistryID + "?sig=secret"}, "full ARM ID"},
		{"missing new project", []string{"--acr-resource-id=" + testExternalRegistryID}, "requires --new-project"},
		{"existing empty ID", []string{"--new-project", "--project-id=", "--acr-resource-id=" +
			testExternalRegistryID}, "--project-id"},
		{"existing empty endpoint", []string{"--new-project", "--project-endpoint=", "--acr-resource-id=" +
			testExternalRegistryID}, "--project-endpoint"},
		{"explicit force false", []string{"--new-project", "--force=false", "--acr-resource-id=" +
			testExternalRegistryID}, "--force"},
		{"Terraform", []string{"--new-project", "--infra=terraform", "--acr-resource-id=" +
			testExternalRegistryID}, "--infra=terraform"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := newProjectAddCommand(&azdext.ExtensionContext{NoPrompt: true})
			command.SetContext(t.Context())
			require.NoError(t, command.ParseFlags(tt.args))
			err := command.RunE(command, nil)
			require.ErrorContains(t, err, tt.want)
			assert.NotContains(t, err.Error(), "daemon")
		})
	}
}

func TestProjectAddACRTerraformManifestBeforeDaemon(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AZD_EXEC_PROJECT_DIR", root)
	raw := []byte("name: test\ninfra:\n  provider: terraform\nservices: {}\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), raw, 0600))
	action := &ProjectAddAction{flags: &projectAddFlags{
		newProject: true, acrResourceIDSet: true, acrResourceID: testExternalRegistryID,
	}}
	require.ErrorContains(t, action.Run(t.Context()), "Terraform")
	assert.Nil(t, action.client)
}

func TestProjectACRManifestRequirements(t *testing.T) {
	tests := []struct {
		name string
		body string
		env  map[string]string
		want string
	}{
		{"pending project", "services:\n  project:\n    host: azure.ai.project\n", nil, ""},
		{"source build", "services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n", nil, ""},
		{"prebuilt", "services:\n  agent:\n    host: azure.ai.agent\n    image: registry/image:v1\n", nil, "prebuilt-image"},
		{"code", "services:\n  agent:\n    host: azure.ai.agent\n    codeConfiguration:\n      runtime: python\n",
			nil, "code"},
		{"prompt", "services:\n  agent:\n    host: azure.ai.agent\n    kind: prompt\n", nil, "non-container"},
		{"legacy prebuilt", "services:\n  agent:\n    host: azure.ai.agent\n    agents:\n" +
			"      - name: test\n        image: registry/image:v1\n", nil, "prebuilt-image"},
		{"conditional source disabled", "services:\n  source:\n    host: azure.ai.agent\n" +
			"    condition: ${BUILD_ENABLED}\n  prebuilt:\n    host: azure.ai.agent\n    image: registry/image:v1\n",
			map[string]string{"BUILD_ENABLED": "false"}, "prebuilt-image"},
		{"conditional source enabled", "services:\n  source:\n    host: azure.ai.agent\n" +
			"    condition: ${BUILD_ENABLED}\n  prebuilt:\n    host: azure.ai.agent\n    image: registry/image:v1\n",
			map[string]string{"BUILD_ENABLED": "true"}, ""},
		{"Terraform", "infra:\n  provider: terraform\nservices: {}\n", nil, "Terraform"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProjectACRManifest([]byte(tt.body), t.TempDir(), tt.env)
			if tt.want != "" {
				require.ErrorContains(t, err, tt.want)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestProjectACRManifestReference(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte(
		"host: azure.ai.agent\nconfig:\n  kind: hosted\n  image: registry/image:v1\n"), 0600))
	raw := []byte("services:\n  agent:\n    $ref: agent.yaml\n")
	// #nosec G304 -- path is within the test project.
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.ErrorContains(t, validateProjectACRManifest(raw, root, nil), "prebuilt-image")
	// #nosec G304 -- path is within the test project.
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestProjectACRSelectionOverridesUndeployedDefaults(t *testing.T) {
	old := map[string]string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": "old-default",
		"AZD_FOUNDRY_ACR_MODE":                 "create",
		"AZURE_AI_PROJECT_ID":                  "old-project",
	}
	project := &resolvedProject{ACRResourceID: testExternalRegistryID}
	plan := planProjectEnvironment(old, projectModeNew, project, true)
	effective := applyProjectEnvironmentPlan(old, plan)
	assert.Equal(t, testExternalRegistryID, effective["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
	assert.Equal(t, "reuse-connect", effective["AZD_FOUNDRY_ACR_MODE"])
	assert.NotContains(t, effective, "AZURE_AI_PROJECT_ID")
	assert.NotContains(t, plan.Unsets, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
	assert.NotContains(t, plan.Unsets, "AZD_FOUNDRY_ACR_MODE")
}

func TestProjectAddACRPendingSelection(t *testing.T) {
	for _, mode := range []string{"create", "none", "reuse-connect"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			client, projectServer, environment, _, _ := newSelfInitializingDeploymentClient(t, root)
			environment.values["AZURE_AI_PROJECT_ID"] = "undeployed-default"
			environment.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testExternalRegistryID + "default"
			environment.values["AZD_FOUNDRY_ACR_MODE"] = mode
			action := &ProjectAddAction{
				client: client,
				flags: &projectAddFlags{
					newProject: true, acrResourceIDSet: true, acrResourceID: testExternalRegistryID,
					noPrompt: true, output: "none",
				},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			require.NoError(t, action.Run(t.Context()))
			assert.Equal(t, testExternalRegistryID, environment.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
			assert.Equal(t, "reuse-connect", environment.values["AZD_FOUNDRY_ACR_MODE"])
			assert.Equal(t, "false", environment.values["USE_EXISTING_AI_PROJECT"])
			assert.Empty(t, environment.values["AZURE_AI_PROJECT_ID"])
			require.Len(t, projectServer.project.Services, 1)
			for _, service := range projectServer.project.Services {
				assert.Equal(t, aiProjectHost, service.GetHost())
			}
		})
	}
}

func TestProjectAddACRPlannedScopeBeforeScaffolding(t *testing.T) {
	for _, layer := range []bool{false, true} {
		t.Run(map[bool]string{false: "root", true: "layer"}[layer], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			group := "rg-test"
			if layer {
				group += "-foundry"
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
					"name: test\ninfra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n"), 0600))
			}
			client, projectServer, environment, _, workflow := newSelfInitializingDeploymentClient(t, root)
			before := maps.Clone(environment.values)
			selector := "/subscriptions/subscription/resourceGroups/" + group +
				"/providers/Microsoft.ContainerRegistry/registries/shared"
			action := &ProjectAddAction{
				client: client,
				flags: &projectAddFlags{
					newProject: true, acrResourceIDSet: true, acrResourceID: selector, noPrompt: true, output: "none",
				},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			require.ErrorContains(t, action.Run(t.Context()), "azd down deletes")
			assert.Empty(t, workflow.args)
			assert.False(t, projectServer.providerSet)
			assert.Empty(t, projectServer.project.Services)
			assert.Equal(t, before, environment.values)
			assert.Empty(t, environment.setLog)
			assert.NoDirExists(t, filepath.Join(root, ".azure"))
			if !layer {
				assert.NoFileExists(t, filepath.Join(root, "azure.yaml"))
			}
		})
	}
}

func TestProjectACRSelectionRollback(t *testing.T) {
	for _, failAt := range []int{0, 1, 2, 3} {
		t.Run(strconv.Itoa(failAt), func(t *testing.T) {
			old := map[string]string{
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": "old-default",
				"AZD_FOUNDRY_ACR_MODE":                 "create",
				"USE_EXISTING_AI_PROJECT":              "true",
			}
			server := &transactionEnvironmentServer{values: maps.Clone(old), failAt: failAt}
			client := newProjectEnvironmentClient(t, server)
			restore, _, err := reconcileProjectEnvironmentWithRollback(
				t.Context(), client, "test", projectModeNew,
				&resolvedProject{ACRResourceID: testExternalRegistryID}, false,
			)
			if failAt == 0 {
				require.NoError(t, err)
				assert.Equal(t, testExternalRegistryID, server.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
				require.NoError(t, restore())
			} else {
				require.Error(t, err)
			}
			assert.Equal(t, old, server.values)
		})
	}
}

func TestProjectAddACRDeployedIdentity(t *testing.T) {
	const projectID = "/subscriptions/subscription/resourceGroups/project/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same binding", true: "different binding"}[changed], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			raw := []byte("name: test\ninfra:\n  provider: microsoft.foundry\nservices:\n" +
				"  project:\n    host: azure.ai.project\n  agent:\n    host: azure.ai.agent\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), raw, 0600))
			client, projectServer, environment, _, _ := newSelfInitializingDeploymentClient(t, root)
			projectServer.project.Infra = &azdext.InfraOptions{Provider: "microsoft.foundry"}
			projectServer.project.Services["project"] = &azdext.ServiceConfig{
				Name: "project", Host: aiProjectHost,
			}
			projectServer.services["project"] = map[string]any{"host": aiProjectHost}
			for key, value := range map[string]string{
				"AZURE_AI_PROJECT_ID": projectID, "AZURE_RESOURCE_GROUP": "project",
				"AZURE_AI_ACCOUNT_NAME": "account", "AZURE_AI_PROJECT_NAME": "project",
				"FOUNDRY_PROJECT_ENDPOINT": "https://account.services.ai.azure.com/api/projects/project",
				"USE_EXISTING_AI_PROJECT":  "false", "AZD_FOUNDRY_ACR_MODE": "reuse-connect",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testExternalRegistryID,
				"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "shared.azurecr.io",
				"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": "registry-connection",
				"AZD_FOUNDRY_ACR_PULL_ASSIGNED":        "true",
			} {
				environment.values[key] = value
			}
			binding, err := json.Marshal(synthesis.ACRBinding{
				Version: 1, Mode: "reuse-connect", RegistryResourceID: testExternalRegistryID,
				ProjectResourceID: projectID, PrincipalID: "principal",
			})
			require.NoError(t, err)
			environment.config = map[string][]byte{synthesis.ACRBindingConfigKey: binding}
			before := maps.Clone(environment.values)
			selector := testExternalRegistryID
			if changed {
				selector += "different"
			}
			action := &ProjectAddAction{
				client: client,
				flags: &projectAddFlags{
					newProject: true, acrResourceIDSet: true, acrResourceID: selector,
					noPrompt: true, output: "none",
				},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			err = action.Run(t.Context())
			if changed {
				require.ErrorContains(t, err, "migration")
				assert.Empty(t, environment.setLog)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, before, environment.values)
			assert.Equal(t, binding, environment.config[synthesis.ACRBindingConfigKey])
			assert.False(t, projectServer.providerSet)
			// #nosec G304 -- path is within the test project.
			persisted, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			assert.Equal(t, raw, persisted)
		})
	}
}

type acrPreflightEnvironmentServer struct {
	transactionEnvironmentServer
	binding []byte
	readErr error
}

func (s *acrPreflightEnvironmentServer) GetConfig(
	context.Context, *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return &azdext.GetConfigResponse{Found: s.binding != nil, Value: s.binding}, nil
}

func TestProjectACRBindingPreflight(t *testing.T) {
	const projectID = "/subscriptions/sub/resourceGroups/project/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	binding, err := json.Marshal(synthesis.ACRBinding{
		Version: 1, Mode: "reuse-connect", RegistryResourceID: testExternalRegistryID,
		ProjectResourceID: projectID, PrincipalID: "principal",
	})
	require.NoError(t, err)
	for _, tt := range []struct {
		name     string
		selector string
		binding  []byte
		readErr  error
		want     string
		envID    string
	}{
		{"same registry", strings.ToUpper(testExternalRegistryID), binding, nil, "", ""},
		{"same registry repairs selector", testExternalRegistryID, binding, nil, "", "manual-change"},
		{"different registry", testExternalRegistryID + "other", binding, nil, "migration", ""},
		{"invalid binding", testExternalRegistryID, []byte(`{"version":3}`), nil, "binding", ""},
		{"unreadable binding", testExternalRegistryID, nil, errors.New("config unavailable"), "config unavailable", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := &acrPreflightEnvironmentServer{
				transactionEnvironmentServer: transactionEnvironmentServer{values: map[string]string{
					"AZURE_AI_PROJECT_ID": projectID, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testExternalRegistryID,
					"AZD_FOUNDRY_ACR_MODE": "reuse-connect",
				}},
				binding: tt.binding, readErr: tt.readErr,
			}
			if tt.envID != "" {
				server.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = tt.envID
			}
			action := &ProjectAddAction{
				client: newProjectEnvironmentClient(t, server),
				flags:  &projectAddFlags{newProject: true, acrResourceIDSet: true, acrResourceID: tt.selector},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			err := action.preflightProjectACR(t.Context(), t.TempDir())
			if tt.want != "" {
				require.ErrorContains(t, err, tt.want)
			} else {
				require.NoError(t, err)
				require.NotNil(t, action.flags.deployedBinding)
				project := &resolvedProject{PreserveDeployed: true, ACRResourceID: tt.selector}
				assert.False(t, projectIdentityChanged(server.values, "", project))
				plan := planProjectEnvironment(server.values, projectModeNew, project, false)
				effective := applyProjectEnvironmentPlan(server.values, plan)
				assert.Equal(t, projectID, effective["AZURE_AI_PROJECT_ID"])
				assert.Equal(t, "reuse-connect", effective["AZD_FOUNDRY_ACR_MODE"])
				assert.Empty(t, plan.Unsets)
			}
			assert.Zero(t, server.calls)
		})
	}
}

func TestProjectACRLegacyOutputsPreserveIdentity(t *testing.T) {
	const projectID = "/subscriptions/sub/resourceGroups/project/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	server := &acrPreflightEnvironmentServer{
		transactionEnvironmentServer: transactionEnvironmentServer{values: map[string]string{
			"AZURE_AI_PROJECT_ID": projectID, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testExternalRegistryID,
			"AZURE_CONTAINER_REGISTRY_ENDPOINT": "shared.azurecr.io", "AZD_FOUNDRY_ACR_MODE": "reuse-connect",
			"AZD_FOUNDRY_ACR_PULL_ASSIGNED": "true", "USE_EXISTING_AI_PROJECT": "false",
		}},
	}
	action := &ProjectAddAction{
		client: newProjectEnvironmentClient(t, server),
		flags:  &projectAddFlags{newProject: true, acrResourceIDSet: true, acrResourceID: testExternalRegistryID},
		extCtx: &azdext.ExtensionContext{Environment: "test"},
	}
	require.NoError(t, action.preflightProjectACR(t.Context(), t.TempDir()))
	assert.Equal(t, projectID, action.flags.preserveProjectID)
	assert.Nil(t, action.flags.deployedBinding)
	project := &resolvedProject{PreserveDeployed: true, ACRResourceID: testExternalRegistryID}
	plan := planProjectEnvironment(server.values, projectModeNew, project, false)
	assert.Equal(t, server.values, applyProjectEnvironmentPlan(server.values, plan))
	assert.Zero(t, server.calls)
}

func TestProjectRegistryOutput(t *testing.T) {
	registry := &projectRegistryOutput{ResourceID: testExternalRegistryID, ExternallyOwned: true}
	data, err := json.Marshal(projectAddOutput{ContainerRegistry: registry})
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, map[string]any{
		"resourceId": testExternalRegistryID, "externallyOwned": true,
	}, result["containerRegistry"])
	var text bytes.Buffer
	writeProjectRegistryOutput(&text, registry)
	assert.Contains(t, text.String(), testExternalRegistryID)
	assert.Contains(t, text.String(), "externally owned")
	text.Reset()
	writeProjectRegistryOutput(&text, nil)
	assert.Empty(t, text.String())
	data, err = json.Marshal(projectAddOutput{})
	require.NoError(t, err)
	assert.NotContains(t, string(data), "containerRegistry")
	for _, format := range []string{"default", "json", "none"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			require.NoError(t, writeProjectAddOutput(&output, format, projectAddOutput{
				ServiceName: "project", Mutation: "unchanged", ContainerRegistry: registry,
			}))
			switch format {
			case "none":
				assert.Empty(t, output.String())
			case "json":
				var parsed map[string]any
				require.NoError(t, json.Unmarshal(output.Bytes(), &parsed))
				assert.Contains(t, parsed, "containerRegistry")
				assert.NotContains(t, output.String(), "Foundry project configuration")
			default:
				assert.Contains(t, output.String(), "externally owned")
				assert.Contains(t, output.String(), testExternalRegistryID)
			}
		})
	}
}

func TestProjectAddACRManifestValidationBeforeMutation(t *testing.T) {
	for _, body := range []string{
		"services:\n  agent:\n    host: azure.ai.agent\n    image: registry/image:v1\n",
		"services:\n  agent:\n    host: azure.ai.agent\n    codeConfiguration:\n      runtime: python\n",
		"infra:\n  provider: terraform\nservices: {}\n",
	} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			path := filepath.Join(root, "azure.yaml")
			require.NoError(t, os.WriteFile(path, []byte(body), 0600))
			old := map[string]string{"AZD_FOUNDRY_ACR_MODE": "create"}
			server := &acrPreflightEnvironmentServer{
				transactionEnvironmentServer: transactionEnvironmentServer{values: maps.Clone(old)},
			}
			action := &ProjectAddAction{
				client: newProjectEnvironmentClient(t, server),
				flags: &projectAddFlags{
					newProject: true, acrResourceIDSet: true, acrResourceID: testExternalRegistryID,
				},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			err := action.Run(t.Context())
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "Unimplemented")
			assert.Zero(t, server.calls)
			assert.Equal(t, old, server.values)
			// #nosec G304 -- path is within the test project.
			persisted, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, body, string(persisted))
			assert.NoDirExists(t, filepath.Join(root, ".azure"))
		})
	}
}
