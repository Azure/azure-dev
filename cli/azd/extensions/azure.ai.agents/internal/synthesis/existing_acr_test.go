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
		selector   string
		group      string
		network    bool
		wantError  string
		outputOnly bool
		preserve   bool
	}{
		{name: "unset"},
		{name: "reuse", selector: id},
		{name: "eject unset", preserve: true},
		{name: "eject reuse", selector: id, preserve: true},
		{name: "eject malformed", selector: "sharedregistry", preserve: true, wantError: "full ARM ID"},
		{name: "eject same group", selector: id, group: "SHARED", preserve: true, wantError: "azd down deletes"},
		{name: "eject private networking reuse", selector: id, network: true, preserve: true},
		{name: "output is not a selector", outputOnly: true},
		{name: "malformed", selector: "sharedregistry", wantError: "full ARM ID"},
		{name: "wrong type", selector: strings.Replace(id, "registries", "other", 1), wantError: "full ARM ID"},
		{name: "child resource", selector: id + "/other/child", wantError: "full ARM ID"},
		{name: "empty subscription", selector: strings.Replace(id, subscription, "", 1), wantError: "full ARM ID"},
		{name: "same group", selector: id, group: "SHARED", wantError: "azd down deletes"},
		{name: "different subscription", selector: strings.Replace(id, subscription, "other-sub", 1), group: "shared"},
		{name: "private networking reuse", selector: id, network: true},
		{name: "private networking creation", network: true, wantError: "auto-created Azure Container Registry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{
				"AZURE_EXISTING_CONTAINER_REGISTRY_RESOURCE_ID": tt.selector,
				"AZURE_SUBSCRIPTION_ID":                         subscription,
				"AZURE_RESOURCE_GROUP":                          "foundry",
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
					expected = "${AZURE_EXISTING_CONTAINER_REGISTRY_RESOURCE_ID}"
				}
				assert.Equal(t, expected, result.Parameters["existingAcrResourceId"])
			} else {
				assert.NotContains(t, result.Parameters, "existingAcrResourceId")
			}
		})
	}
}

func TestSynthesizeExistingRegistryEnvironment(t *testing.T) {
	const id = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/sharedregistry"
	t.Setenv("AZURE_EXISTING_CONTAINER_REGISTRY_RESOURCE_ID", id)
	input := Input{
		RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n"),
		ServiceName:  "project",
	}
	result, err := Synthesize(input)
	require.NoError(t, err)
	assert.Equal(t, id, result.Parameters["existingAcrResourceId"])
	input.Env = map[string]string{"AZURE_EXISTING_CONTAINER_REGISTRY_RESOURCE_ID": ""}
	result, err = Synthesize(input)
	require.NoError(t, err)
	assert.NotContains(t, result.Parameters, "existingAcrResourceId")

	input.Env = map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUB",
	}
	input.ResourceGroupName = "SHARED"
	_, err = Synthesize(input)
	require.ErrorContains(t, err, "azd down deletes")
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
	for _, resource := range resources.Resources {
		switch resource.Name {
		case "acr":
			assert.Equal(t, "[and(parameters('includeAcr'), empty(parameters('existingAcrResourceId')))]", resource.Condition)
			found["acr"] = true
		case "existing-acr", "existing-acr-connection":
			assert.Equal(t, "[and(parameters('includeAcr'), not(empty(parameters('existingAcrResourceId'))))]", resource.Condition)
			found[resource.Name] = true
		}
	}
	assert.Len(t, found, 3)
}
