// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSynthesizeExistingRegistry(t *testing.T) {
	const subscription = "11111111-1111-1111-1111-111111111111"
	const id = "/subscriptions/" + subscription + "/resourceGroups/shared/" +
		"providers/Microsoft.ContainerRegistry/registries/sharedregistry"
	const raw = `services:
  my-project:
    host: azure.ai.project
  assistant:
    host: azure.ai.agent
    kind: hosted
`
	tests := []struct {
		name       string
		mode       string
		selector   string
		group      string
		network    bool
		wantError  string
		outputOnly bool
		preserve   bool
	}{
		{name: "unset"},
		{name: "environment selector", selector: id},
		{name: "environment selector overrides undeployed create", mode: "create", selector: id},
		{name: "reuse", mode: "reuse-connect", selector: id},
		{name: "eject unset", preserve: true},
		{name: "eject reuse", mode: "reuse-connect", selector: id, preserve: true},
		{name: "eject malformed", mode: "reuse-connect", selector: "sharedregistry", preserve: true, wantError: "full ARM ID"},
		{name: "eject same group", mode: "reuse-connect", selector: id, group: "SHARED",
			preserve: true, wantError: "azd down deletes"},
		{name: "eject private networking reuse", mode: "reuse-connect", selector: id, network: true, preserve: true},
		{name: "legacy deployment outputs", outputOnly: true},
		{name: "create preserves deployment outputs", mode: "create", outputOnly: true},
		{name: "eject preserves deployment outputs", mode: "create", outputOnly: true, preserve: true},
		{name: "reuse missing ID", mode: "reuse-connect", wantError: "requires AZURE_CONTAINER_REGISTRY_RESOURCE_ID"},
		{name: "unsupported mode", mode: "already-connected", wantError: "AZD_FOUNDRY_ACR_MODE"},
		{name: "none becomes create when a registry is required", mode: "none"},
		{name: "malformed", mode: "reuse-connect", selector: "sharedregistry", wantError: "full ARM ID"},
		{name: "wrong type", mode: "reuse-connect",
			selector: strings.Replace(id, "registries", "other", 1), wantError: "full ARM ID"},
		{name: "child resource", mode: "reuse-connect", selector: id + "/other/child", wantError: "full ARM ID"},
		{name: "empty subscription", mode: "reuse-connect",
			selector: strings.Replace(id, subscription, "", 1), wantError: "full ARM ID"},
		{name: "same group", mode: "reuse-connect", selector: id, group: "SHARED", wantError: "azd down deletes"},
		{name: "same group without mode", selector: id, group: "SHARED", wantError: "azd down deletes"},
		{name: "same group with create intent", mode: "create", selector: id, group: "SHARED",
			wantError: "azd down deletes"},
		{name: "different subscription", mode: "reuse-connect",
			selector: strings.Replace(id, subscription, "other-sub", 1), group: "shared",
			wantError: "cross-subscription registry reuse is not supported"},
		{name: "different subscription environment selector",
			selector: strings.Replace(id, subscription, "other-sub", 1), wantError: "same subscription"},
		{name: "eject different subscription", mode: "reuse-connect", preserve: true,
			selector: strings.Replace(id, subscription, "other-sub", 1), wantError: "same subscription"},
		{name: "private networking reuse", mode: "reuse-connect", selector: id, network: true},
		{name: "private networking creation", network: true, wantError: "auto-created Azure Container Registry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{
				"AZD_FOUNDRY_ACR_MODE":                 tt.mode,
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": tt.selector,
				"AZURE_SUBSCRIPTION_ID":                subscription,
				"AZURE_RESOURCE_GROUP":                 "foundry",
			}
			if tt.outputOnly {
				env["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = strings.Replace(id, "/shared/", "/foundry/", 1)
				env["AZURE_AI_PROJECT_ID"] = "/subscriptions/" + subscription + "/resourceGroups/foundry/" +
					"providers/Microsoft.CognitiveServices/accounts/account/projects/project"
				env["AZURE_CONTAINER_REGISTRY_ENDPOINT"] = "sharedregistry.azurecr.io"
			}
			yaml := raw
			if tt.network {
				yaml = strings.Replace(yaml, "host: azure.ai.project", `host: azure.ai.project
    network:
      peSubnet:
        vnet: /subscriptions/`+subscription+`/resourceGroups/network/providers/Microsoft.Network/virtualNetworks/vnet
        name: pe-subnet`, 1)
			}
			result, err := Synthesize(Input{
				RawAzureYAML: []byte(yaml), ServiceName: "my-project",
				Env: env, ResourceGroupName: tt.group,
				PreserveVarRefs: tt.preserve,
			})
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, true, result.Parameters["includeAcr"])
			if tt.selector != "" {
				expected := tt.selector
				if tt.preserve {
					expected = "${AZURE_CONTAINER_REGISTRY_RESOURCE_ID}"
				}
				assert.Equal(t, expected, result.Parameters["existingAcrResourceId"])
			} else {
				assert.NotContains(t, result.Parameters, "existingAcrResourceId")
			}
		})
	}
}

func TestSynthesizeRegistryUsesOnlySnapshotAndMode(t *testing.T) {
	const id = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/sharedregistry"
	t.Setenv("AZURE_CONTAINER_REGISTRY_RESOURCE_ID", id)
	input := Input{
		RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n"),
		ServiceName:  "project",
	}
	result, err := Synthesize(input)
	require.NoError(t, err)
	assert.NotContains(t, result.Parameters, "existingAcrResourceId")
	input.Env = map[string]string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": id}
	result, err = Synthesize(input)
	require.NoError(t, err)
	assert.NotContains(t, result.Parameters, "existingAcrResourceId")
	input.Env["AZURE_SUBSCRIPTION_ID"] = "other"
	_, err = Synthesize(input)
	require.ErrorContains(t, err, "same subscription")
	input.Env["AZURE_SUBSCRIPTION_ID"] = "sub"
	input.ResourceGroupName = "shared"
	_, err = Synthesize(input)
	require.ErrorContains(t, err, "azd down deletes")

	input.RawAzureYAML = []byte("services:\n  project:\n    host: azure.ai.project\n" +
		"  agent:\n    host: azure.ai.agent\n    kind: hosted\n")
	input.Env = map[string]string{
		"AZURE_SUBSCRIPTION_ID":                "SUB",
		"AZD_FOUNDRY_ACR_MODE":                 "reuse-connect",
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": id,
	}
	input.ResourceGroupName = "SHARED"
	_, err = Synthesize(input)
	require.ErrorContains(t, err, "azd down deletes")
	input.Env["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = ""
	_, err = Synthesize(input)
	require.ErrorContains(t, err, "requires AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
}

func TestGreenfieldACRMode(t *testing.T) {
	const id = "/subscriptions/sub/resourceGroups/foundry/providers/Microsoft.ContainerRegistry/registries/registry"
	const projectID = "/subscriptions/sub/resourceGroups/foundry/" +
		"providers/Microsoft.CognitiveServices/accounts/account/projects/project"
	for _, tt := range []struct {
		name, mode, id, group, project, endpoint, want string
		binding                                        *ACRBinding
	}{
		{name: "explicit reuse", mode: "reuse-connect", id: id, group: "foundry", want: "reuse-connect"},
		{name: "malformed reuse is validated by synthesis", mode: "reuse-connect", id: "invalid",
			group: "foundry", want: "reuse-connect"},
		{name: "fresh in-group input", id: id, group: "foundry", want: "reuse-connect"},
		{name: "fresh in-group create intent", mode: "create", id: id, group: "foundry", want: "reuse-connect"},
		{name: "malformed create intent", mode: "create", id: "created-output",
			group: "foundry", want: "reuse-connect"},
		{name: "external input after none", mode: "none", id: id, group: "external", want: "reuse-connect"},
		{name: "no input", group: "foundry", want: "create"},
		{name: "external input", id: id, group: "external", want: "reuse-connect"},
		{name: "legacy deployment outputs", id: strings.ToUpper(id), group: "foundry",
			project: projectID, endpoint: "registry.azurecr.io", want: "create"},
		{name: "project ID alone is not ownership", id: id, group: "foundry",
			project: projectID, want: "reuse-connect"},
		{name: "endpoint alone is not ownership", id: id, group: "foundry",
			endpoint: "registry.azurecr.io", want: "reuse-connect"},
		{name: "mismatched project outputs", id: id, group: "foundry",
			project:  strings.Replace(projectID, "/foundry/", "/other/", 1),
			endpoint: "registry.azurecr.io", want: "reuse-connect"},
		{name: "mismatched registry output", id: id, group: "foundry",
			project: projectID, endpoint: "other.azurecr.io", want: "reuse-connect"},
		{name: "binding without legacy output variables", id: id, group: "foundry",
			binding: &ACRBinding{Mode: "create", RegistryResourceID: id}, want: "create"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mode, err := GreenfieldACRMode(Input{
				Env: map[string]string{
					"AZD_FOUNDRY_ACR_MODE":                 tt.mode,
					"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": tt.id,
					"AZURE_SUBSCRIPTION_ID":                "sub",
					"AZURE_AI_PROJECT_ID":                  tt.project,
					"AZURE_CONTAINER_REGISTRY_ENDPOINT":    tt.endpoint,
				},
				ResourceGroupName: tt.group,
				ACRBinding:        tt.binding,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, mode)
		})
	}
}

func TestARMExistingRegistryShape(t *testing.T) {
	raw, err := ARMTemplate()
	require.NoError(t, err)
	type template struct {
		Parameters map[string]struct {
			DefaultValue any `json:"defaultValue"`
		} `json:"parameters"`
		Resources map[string]struct {
			Name       string `json:"name"`
			Type       string `json:"type"`
			Condition  string `json:"condition"`
			Properties struct {
				Template   json.RawMessage `json:"template"`
				Parameters map[string]any  `json:"parameters"`
			} `json:"properties"`
		} `json:"resources"`
	}
	var main template
	require.NoError(t, json.Unmarshal(raw, &main))
	require.Contains(t, main.Parameters, "existingAcrResourceId")
	assert.Equal(t, "", main.Parameters["existingAcrResourceId"].DefaultValue)
	require.Len(t, main.Resources, 2)
	var resources template
	require.NoError(t, json.Unmarshal(main.Resources["resources"].Properties.Template, &resources))
	found := map[string]bool{}
	for name, resource := range resources.Resources {
		switch name {
		case "acr":
			assert.Equal(t, "[and(parameters('includeAcr'), empty(parameters('existingAcrResourceId')))]", resource.Condition)
			found["acr"] = true
		case "existingAcr", "existingAcrConnection":
			expectedCondition := "[and(parameters('includeAcr'), not(empty(parameters('existingAcrResourceId'))))]"
			if name == "existingAcr" {
				expectedCondition = "[and(and(parameters('includeAcr'), " +
					"not(empty(parameters('existingAcrResourceId')))), not(parameters('acrPullAssigned')))]"
			}
			assert.Equal(t, expectedCondition, resource.Condition)
			prefix := "existing-acr"
			if name == "existingAcrConnection" {
				prefix += "-connection"
			}
			expectedName := "[format('" + prefix + "-{0}', uniqueString(" +
				"resourceId('Microsoft.CognitiveServices/accounts', variables('foundryAccountName')), " +
				"resourceId('Microsoft.CognitiveServices/accounts/projects', " +
				"variables('foundryAccountName'), parameters('foundryProjectName'))))]"
			if name == "existingAcr" {
				expectedName = "[if(empty(parameters('acrAccessOperationId')), " +
					strings.TrimSuffix(strings.TrimPrefix(expectedName, "["), "]") +
					", format('azd-acr-{0}', parameters('acrAccessOperationId')))]"
			}
			assert.Equal(t, expectedName, resource.Name)
			if name == "existingAcrConnection" {
				condition := "and(parameters('includeAcr'), not(empty(parameters('existingAcrResourceId'))))"
				assert.Equal(t, "[if("+condition+", createObject('value', "+
					"reference('externalRegistry').loginServer), createObject('value', ''))]",
					resource.Properties.Parameters["acrEndpoint"])
				assert.Equal(t, "[if("+condition+", createObject('value', "+
					"extensionResourceId(format('/subscriptions/{0}/resourceGroups/{1}', "+
					"split(parameters('existingAcrResourceId'), '/')[2], "+
					"split(parameters('existingAcrResourceId'), '/')[4]), "+
					"'Microsoft.ContainerRegistry/registries', "+
					"last(split(parameters('existingAcrResourceId'), '/')))), createObject('value', ''))]",
					resource.Properties.Parameters["acrResourceId"])
			}
			found[name] = true
		}
	}
	assert.Len(t, found, 3)
}

func TestARMExistingRegistrySelectsPullRoleByPermissionMode(t *testing.T) {
	raw, err := ARMTemplate()
	require.NoError(t, err)
	body := string(raw)
	assert.Contains(t, body, "7f951dda-4ed3-4680-a7ca-43fe172d538d")
	assert.Contains(t, body, "b93aa761-3e63-49ed-ac28-beffa264f7ac")
	assert.Contains(t, body, "AbacRepositoryPermissions")
	assert.Contains(t, body, "roleAssignmentMode")
	assert.Contains(t, body, "2025-11-01")
}

func TestARMExistingRegistryRoleLookupIsLazy(t *testing.T) {
	for _, tt := range []struct {
		name  string
		read  func() ([]byte, error)
		roles int
	}{
		{"greenfield", ARMTemplate, 1},
		{"existing project", ExistingProjectARMTemplate, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := tt.read()
			require.NoError(t, err)
			var template any
			require.NoError(t, json.Unmarshal(raw, &template))
			roles := 0
			var inspect func(any)
			inspect = func(value any) {
				switch node := value.(type) {
				case map[string]any:
					if role, exists := node["roleDefinitionId"]; exists {
						if parameter, ok := role.(map[string]any); ok {
							role = parameter["value"]
						}
						if expression, ok := role.(string); ok && strings.Contains(expression, "roleAssignmentMode") {
							roles++
							assert.Equal(t, "[if(equals(parameters('mode'), 'reuse-connect'), "+
								"if(equals(reference(resourceId('Microsoft.ContainerRegistry/registries', "+
								"parameters('registryName')), '2025-11-01').roleAssignmentMode, "+
								"'AbacRepositoryPermissions'), createObject('value', variables('repositoryReaderRoleId')), "+
								"createObject('value', variables('acrPullRoleId'))), "+
								"createObject('value', variables('acrPullRoleId')))]", expression)
						}
					}
					for _, child := range node {
						inspect(child)
					}
				case []any:
					for _, child := range node {
						inspect(child)
					}
				}
			}
			inspect(template)
			assert.Equal(t, tt.roles, roles)
		})
	}
}
