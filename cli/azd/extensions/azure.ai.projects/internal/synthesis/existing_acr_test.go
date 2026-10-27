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
		{name: "reuse", mode: "reuse-connect", selector: id},
		{name: "eject unset", preserve: true},
		{name: "eject reuse", mode: "reuse-connect", selector: id, preserve: true},
		{name: "eject malformed", mode: "reuse-connect", selector: "sharedregistry", preserve: true, wantError: "full ARM ID"},
		{name: "eject same group", mode: "reuse-connect", selector: id, group: "SHARED",
			preserve: true, wantError: "azd down deletes"},
		{name: "eject private networking reuse", mode: "reuse-connect", selector: id, network: true, preserve: true},
		{name: "output is not a selector", outputOnly: true},
		{name: "create ignores output", mode: "create", outputOnly: true},
		{name: "eject create ignores output", mode: "create", outputOnly: true, preserve: true},
		{name: "reuse missing ID", mode: "reuse-connect", wantError: "requires AZURE_CONTAINER_REGISTRY_RESOURCE_ID"},
		{name: "unsupported mode", mode: "already-connected", wantError: "AZD_FOUNDRY_ACR_MODE"},
		{name: "none with registry required", mode: "none", wantError: "AZD_FOUNDRY_ACR_MODE"},
		{name: "malformed", mode: "reuse-connect", selector: "sharedregistry", wantError: "full ARM ID"},
		{name: "wrong type", mode: "reuse-connect",
			selector: strings.Replace(id, "registries", "other", 1), wantError: "full ARM ID"},
		{name: "child resource", mode: "reuse-connect", selector: id + "/other/child", wantError: "full ARM ID"},
		{name: "empty subscription", mode: "reuse-connect",
			selector: strings.Replace(id, subscription, "", 1), wantError: "full ARM ID"},
		{name: "same group", mode: "reuse-connect", selector: id, group: "SHARED", wantError: "azd down deletes"},
		{name: "different subscription", mode: "reuse-connect",
			selector: strings.Replace(id, subscription, "other-sub", 1), group: "shared"},
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
				env["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = id
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
	for _, tt := range []struct {
		mode string
		id   string
		want string
	}{
		{want: "create"},
		{id: "registry", want: "reuse-connect"},
		{mode: "create", id: "created-output", want: "create"},
		{mode: "reuse-connect", id: "registry", want: "reuse-connect"},
		{mode: "already-connected", want: "already-connected"},
		// Legacy env: azd-created registry output in the Foundry group, no mode yet.
		{id: "/subscriptions/sub/resourceGroups/rg-foundry/providers/Microsoft.ContainerRegistry/registries/cr",
			want: "create"},
		{id: "/subscriptions/sub/resourceGroups/rg-shared/providers/Microsoft.ContainerRegistry/registries/cr",
			want: "reuse-connect"},
		{id: "/subscriptions/other/resourceGroups/rg-foundry/providers/Microsoft.ContainerRegistry/registries/cr",
			want: "reuse-connect"},
	} {
		t.Run(tt.mode+"/"+tt.id, func(t *testing.T) {
			assert.Equal(t, tt.want, GreenfieldACRMode(map[string]string{
				"AZD_FOUNDRY_ACR_MODE": tt.mode, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": tt.id,
				"AZURE_SUBSCRIPTION_ID": "sub",
			}, "rg-foundry"))
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
				Template json.RawMessage `json:"template"`
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
