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
