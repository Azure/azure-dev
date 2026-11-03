// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

const testInitACRID = "/subscriptions/sub/resourceGroups/registry-rg/providers/" +
	"Microsoft.ContainerRegistry/registries/shared"

type initACREnvironmentServer struct {
	testEnvironmentServiceServer
	binding []byte
}

func (s *initACREnvironmentServer) List(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentListResponse, error) {
	return &azdext.EnvironmentListResponse{Environments: []*azdext.EnvironmentDescription{
		{Name: "test", Default: true},
	}}, nil
}

func (s *initACREnvironmentServer) GetConfig(
	context.Context, *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	return &azdext.GetConfigResponse{Found: s.binding != nil, Value: s.binding}, nil
}

func TestInitACRBindingPreflight(t *testing.T) {
	for _, tt := range []struct {
		name     string
		mode     string
		selected string
		wantErr  bool
	}{
		{"same registry", "reuse-connect", testInitACRID, false},
		{"different registry", "reuse-connect", testInitACRID + "other", true},
		{"create to reuse", "create", testInitACRID, true},
		{"explicit empty selector", "reuse-connect", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n" +
				"    host: azure.ai.project\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
			binding, err := json.Marshal(synthesis.ACRBinding{
				Version: 1, Mode: tt.mode, RegistryResourceID: testInitACRID,
				ProjectResourceID: "/subscriptions/sub/resourceGroups/rg/providers/" +
					"Microsoft.CognitiveServices/accounts/account/projects/project",
				PrincipalID: "principal",
			})
			require.NoError(t, err)
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
						"AZD_FOUNDRY_ACR_MODE":                 tt.mode,
					}},
				},
				binding: binding,
			}

			client := newTestAzdClient(t, server, &recordingProjectWorkflowServer{})
			err = validateInitACRProject(t.Context(), client, &initFlags{
				acrResourceIDSet: true, acrResourceID: tt.selected,
			})
			if tt.wantErr {
				if tt.selected == "" {
					require.ErrorContains(t, err, "--acr-resource-id")
				} else {
					require.ErrorContains(t, err, "migration")
				}
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, server.setKeys)
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, after)
		})
	}
}

func TestInitACRRejectsCrossSubscriptionBeforeWrites(t *testing.T) {
	for _, template := range []string{"", "template.yaml"} {
		t.Run(template, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n" +
				"    host: azure.ai.project\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_SUBSCRIPTION_ID": "other-subscription",
						"AZURE_RESOURCE_GROUP":  "foundry-rg",
					}},
				},
			}
			client := newTestAzdClient(t, server, &recordingProjectWorkflowServer{})
			err := validateInitACRProject(t.Context(), client, &initFlags{
				acrResourceIDSet: true, acrResourceID: testInitACRID, templatePointer: template,
			})
			require.ErrorContains(t, err, "same subscription")
			require.Empty(t, server.setKeys)
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, after)
			require.NoDirExists(t, filepath.Join(root, ".azure"))
		})
	}
}

func TestInitACRRejectsTeardownScopeBeforeWrites(t *testing.T) {
	for _, tt := range []struct {
		name       string
		infra      string
		rootGroup  string
		layerGroup string
		wantErr    bool
	}{
		{"root group", "", "registry-rg", "", true},
		{"layer group", "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n",
			"application-rg", "registry-rg", true},
		{"other layer root", "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n",
			"registry-rg", "foundry-rg", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\n" + tt.infra + "services:\n  foundry:\n" +
				"    host: azure.ai.project\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_SUBSCRIPTION_ID":        "sub",
						"AZURE_RESOURCE_GROUP":         tt.rootGroup,
						"AZURE_FOUNDRY_RESOURCE_GROUP": tt.layerGroup,
					}},
				},
			}

			client := newTestAzdClient(t, server, &recordingProjectWorkflowServer{})
			err := validateInitACRProject(t.Context(), client, &initFlags{
				acrResourceIDSet: true, acrResourceID: testInitACRID,
			})
			if tt.wantErr {
				require.ErrorContains(t, err, "resource group")
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, server.setKeys)
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, after)
		})
	}
}

func TestInitACREnvironmentSelectorPreflight(t *testing.T) {
	for _, tt := range []struct {
		name       string
		layer      bool
		rootGroup  string
		layerGroup string
		registry   string
		binding    bool
		wantErr    bool
	}{
		{name: "retained root group", rootGroup: "registry-rg", registry: testInitACRID, wantErr: true},
		{name: "external root registry", rootGroup: "foundry-rg", registry: testInitACRID},
		{name: "retained layer group", layer: true, rootGroup: "application-rg",
			layerGroup: "registry-rg", registry: testInitACRID, wantErr: true},
		{name: "layer can reuse root registry", layer: true, rootGroup: "registry-rg",
			layerGroup: "foundry-rg", registry: testInitACRID},
		{name: "default root group", registry: strings.Replace(testInitACRID, "/registry-rg/", "/rg-test/", 1),
			wantErr: true},
		{name: "default layer group", layer: true,
			registry: strings.Replace(testInitACRID, "/registry-rg/", "/rg-test-foundry/", 1), wantErr: true},
		{name: "canonical retained root group", rootGroup: "registry-rg", registry: testInitACRID,
			binding: true, wantErr: true},
		{name: "canonical external root registry", rootGroup: "foundry-rg", registry: testInitACRID, binding: true},
		{name: "canonical default root group", binding: true,
			registry: strings.Replace(testInitACRID, "/registry-rg/", "/rg-test/", 1), wantErr: true},
		{name: "canonical default layer group", layer: true, binding: true,
			registry: strings.Replace(testInitACRID, "/registry-rg/", "/rg-test-foundry/", 1), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := "name: test\nservices:\n  foundry:\n    host: azure.ai.project\n"
			if tt.layer {
				content += "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n"
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(content), 0o600))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_SUBSCRIPTION_ID":                "sub",
						"AZURE_LOCATION":                       "eastus2",
						"AZURE_RESOURCE_GROUP":                 tt.rootGroup,
						"AZURE_FOUNDRY_RESOURCE_GROUP":         tt.layerGroup,
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": tt.registry,
						"AZD_FOUNDRY_ACR_MODE":                 "reuse-connect",
					}},
				},
			}
			if tt.binding {
				binding, err := json.Marshal(synthesis.ACRBinding{
					Version: 1, Mode: "reuse-connect", RegistryResourceID: tt.registry,
					ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
						"Microsoft.CognitiveServices/accounts/account/projects/project",
					PrincipalID: "principal",
				})
				require.NoError(t, err)
				server.binding = binding
				delete(server.values["test"], "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
				delete(server.values["test"], "AZD_FOUNDRY_ACR_MODE")
			}
			before := maps.Clone(server.values["test"])
			workflow := &recordingProjectWorkflowServer{}
			client := newTestAzdClient(t, server, workflow)
			err := validateInitACRProject(t.Context(), client, &initFlags{})
			if tt.wantErr {
				require.ErrorContains(t, err, "azd down deletes")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, before, server.values["test"])
			require.Empty(t, server.setKeys)
			require.Empty(t, workflow.requests)
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, string(after))
		})
	}
}

func TestInitACREnvironmentSelectorRejectsBeforeSourceInit(t *testing.T) {
	for _, canonical := range []bool{false, true} {
		t.Run(fmt.Sprint("canonical=", canonical), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
			source := filepath.Join(root, "src")
			require.NoError(t, os.Mkdir(source, 0o750))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_SUBSCRIPTION_ID":                "sub",
						"AZURE_LOCATION":                       "eastus2",
						"AZURE_RESOURCE_GROUP":                 "registry-rg",
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
					}},
				},
			}
			if canonical {
				binding, err := json.Marshal(synthesis.ACRBinding{
					Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
					ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
						"Microsoft.CognitiveServices/accounts/account/projects/project",
					PrincipalID: "principal",
				})
				require.NoError(t, err)
				server.binding = binding
				delete(server.values["test"], "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
			}
			before := maps.Clone(server.values["test"])
			workflow := &recordingProjectWorkflowServer{}
			t.Setenv("AZD_SERVER", newTestAzdServer(t, server, workflow))
			cmd := newInitCommand(&azdext.ExtensionContext{NoPrompt: true, Environment: "test"})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--src", source})
			require.ErrorContains(t, cmd.Execute(), "azd down deletes")
			require.Equal(t, before, server.values["test"])
			require.Empty(t, server.setKeys)
			require.Empty(t, workflow.requests)
			//nolint:gosec // Test-owned path.
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, after)
			require.NoDirExists(t, filepath.Join(root, ".azure"))
		})
	}
}

func TestInitACREnvironmentSelectorPreservesOtherProjectFlows(t *testing.T) {
	for _, flow := range []string{
		"explicit existing", "saved existing", "endpoint", "referenced endpoint", "legacy create",
	} {
		t.Run(flow, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := "name: test\nservices:\n  foundry:\n    host: azure.ai.project\n"
			values := map[string]string{
				"AZURE_SUBSCRIPTION_ID":                "sub",
				"AZURE_RESOURCE_GROUP":                 "registry-rg",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
			}
			flags := &initFlags{}
			switch flow {
			case "explicit existing":
				flags.projectResourceId = "existing-project"
			case "saved existing":
				values["USE_EXISTING_AI_PROJECT"] = "true"
			case "endpoint", "referenced endpoint":
				values["AZD_FOUNDRY_ACR_MODE"] = "already-connected"
				if flow == "endpoint" {
					content += "    endpoint: https://account.services.ai.azure.com/api/projects/project\n"
				} else {
					content += "    $ref: project.yaml\n"
					require.NoError(t, os.WriteFile(filepath.Join(root, "project.yaml"), []byte(
						"endpoint: https://account.services.ai.azure.com/api/projects/project\n"), 0o600))
				}
			case "legacy create":
				values["AZURE_AI_PROJECT_ID"] = "/subscriptions/sub/resourceGroups/registry-rg/" +
					"providers/Microsoft.CognitiveServices/accounts/account/projects/project"
				values["AZURE_CONTAINER_REGISTRY_ENDPOINT"] = "shared.azurecr.io"
				values["AZD_FOUNDRY_ACR_MODE"] = "create"
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(content), 0o600))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": values},
				},
			}
			client := newTestAzdClient(t, server, &recordingProjectWorkflowServer{})
			require.NoError(t, validateInitACRProject(t.Context(), client, flags))
			require.Empty(t, server.setKeys)
		})
	}
}
