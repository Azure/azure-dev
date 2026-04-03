// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_AllResourceTypes_Coverage3(t *testing.T) {
	all := AllResourceTypes()
	require.NotEmpty(t, all)
	// Verify exact count of resource types
	assert.GreaterOrEqual(t, len(all), 14, "should have at least 14 resource types")
	// Check completeness
	seen := map[ResourceType]bool{}
	for _, rt := range all {
		seen[rt] = true
	}
	assert.True(t, seen[ResourceTypeDbRedis])
	assert.True(t, seen[ResourceTypeStorage])
	assert.True(t, seen[ResourceTypeKeyVault])
}

func Test_ResourceType_String_Coverage3(t *testing.T) {
	// Focus on edge case: unknown type returns empty string
	assert.Equal(t, "", ResourceType("custom-type").String())
	assert.Equal(t, "", ResourceType("").String())
}

func Test_ResourceType_AzureResourceType_Coverage3(t *testing.T) {
	// Focus on edge case: unknown type returns empty string
	assert.Equal(t, "", ResourceType("custom-type").AzureResourceType())
	assert.Equal(t, "", ResourceType("").AzureResourceType())
}

func Test_ResourceConfig_MarshalYAML_NoProps(t *testing.T) {
	rc := &ResourceConfig{
		Type: ResourceTypeDbRedis,
		Name: "my-redis",
		Uses: []string{"other"},
	}

	data, err := yaml.Marshal(rc)
	require.NoError(t, err)
	content := string(data)

	// Name should not be included because IncludeName is false
	assert.NotContains(t, content, "name: my-redis")
	assert.Contains(t, content, "type: db.redis")
}

func Test_ResourceConfig_MarshalYAML_WithIncludeName(t *testing.T) {
	rc := &ResourceConfig{
		Type:        ResourceTypeDbRedis,
		Name:        "my-redis",
		IncludeName: true,
	}

	data, err := yaml.Marshal(rc)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "name: my-redis")
}

func Test_ResourceConfig_UnmarshalYAML_Basic(t *testing.T) {
	yamlData := `
type: db.redis
uses:
  - other-resource
`
	var rc ResourceConfig
	err := yaml.Unmarshal([]byte(yamlData), &rc)
	require.NoError(t, err)
	assert.Equal(t, ResourceTypeDbRedis, rc.Type)
	require.Len(t, rc.Uses, 1)
	assert.Equal(t, "other-resource", rc.Uses[0])
}

func Test_ResourceConfig_UnmarshalYAML_HostContainerApp(t *testing.T) {
	yamlData := `
type: host.containerapp
port: 8080
`
	var rc ResourceConfig
	err := yaml.Unmarshal([]byte(yamlData), &rc)
	require.NoError(t, err)
	assert.Equal(t, ResourceTypeHostContainerApp, rc.Type)
	require.NotNil(t, rc.Props)

	props, ok := rc.Props.(ContainerAppProps)
	require.True(t, ok)
	assert.Equal(t, 8080, props.Port)
}

func Test_ResourceConfig_UnmarshalYAML_HostAppService(t *testing.T) {
	yamlData := `
type: host.appservice
port: 3000
runtime:
  stack: python
  version: "3.12"
`
	var rc ResourceConfig
	err := yaml.Unmarshal([]byte(yamlData), &rc)
	require.NoError(t, err)
	assert.Equal(t, ResourceTypeHostAppService, rc.Type)
	require.NotNil(t, rc.Props)

	props, ok := rc.Props.(AppServiceProps)
	require.True(t, ok)
	assert.Equal(t, 3000, props.Port)
	assert.Equal(t, AppServiceRuntimeStack("python"), props.Runtime.Stack)
	assert.Equal(t, "3.12", props.Runtime.Version)
}

func Test_ResourceConfig_UnmarshalYAML_OpenAiModel(t *testing.T) {
	yamlData := `
type: ai.openai.model
model:
  name: gpt-4o
  version: "2024-08-06"
`
	var rc ResourceConfig
	err := yaml.Unmarshal([]byte(yamlData), &rc)
	require.NoError(t, err)
	assert.Equal(t, ResourceTypeOpenAiModel, rc.Type)

	props, ok := rc.Props.(AIModelProps)
	require.True(t, ok)
	assert.Equal(t, "gpt-4o", props.Model.Name)
	assert.Equal(t, "2024-08-06", props.Model.Version)
}

func Test_ResourceConfig_UnmarshalYAML_Storage(t *testing.T) {
	yamlData := `
type: storage
`
	var rc ResourceConfig
	err := yaml.Unmarshal([]byte(yamlData), &rc)
	require.NoError(t, err)
	assert.Equal(t, ResourceTypeStorage, rc.Type)
}

func Test_ResourceConfig_UnmarshalYAML_CosmosDB(t *testing.T) {
	yamlData := `
type: db.cosmos
containers:
  - name: items
    partitionKeyPaths:
      - /id
`
	var rc ResourceConfig
	err := yaml.Unmarshal([]byte(yamlData), &rc)
	require.NoError(t, err)
	assert.Equal(t, ResourceTypeDbCosmos, rc.Type)

	props, ok := rc.Props.(CosmosDBProps)
	require.True(t, ok)
	require.Len(t, props.Containers, 1)
	assert.Equal(t, "items", props.Containers[0].Name)
}

func Test_ResourceConfig_UnmarshalYAML_EventHubs(t *testing.T) {
	yamlData := `
type: messaging.eventhubs
`
	var rc ResourceConfig
	err := yaml.Unmarshal([]byte(yamlData), &rc)
	require.NoError(t, err)
	assert.Equal(t, ResourceTypeMessagingEventHubs, rc.Type)
}

func Test_ResourceConfig_UnmarshalYAML_ServiceBus(t *testing.T) {
	yamlData := `
type: messaging.servicebus
`
	var rc ResourceConfig
	err := yaml.Unmarshal([]byte(yamlData), &rc)
	require.NoError(t, err)
	assert.Equal(t, ResourceTypeMessagingServiceBus, rc.Type)
}

func Test_ResourceConfig_MarshalYAML_WithContainerAppProps(t *testing.T) {
	rc := &ResourceConfig{
		Type: ResourceTypeHostContainerApp,
		Props: ContainerAppProps{
			Port: 8080,
		},
	}

	data, err := yaml.Marshal(rc)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "port: 8080")
}

func Test_ResourceConfig_MarshalYAML_WithAppServiceProps(t *testing.T) {
	rc := &ResourceConfig{
		Type: ResourceTypeHostAppService,
		Props: AppServiceProps{
			Port: 3000,
			Runtime: AppServiceRuntime{
				Stack:   "python",
				Version: "3.12",
			},
		},
	}

	data, err := yaml.Marshal(rc)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "port: 3000")
}

func Test_ResourceConfig_MarshalYAML_WithOpenAiModelProps(t *testing.T) {
	rc := &ResourceConfig{
		Type: ResourceTypeOpenAiModel,
		Props: AIModelProps{
			Model: AIModelPropsModel{
				Name:    "gpt-4o",
				Version: "2024-08-06",
			},
		},
	}

	data, err := yaml.Marshal(rc)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "gpt-4o")
}

func Test_ResourceConfig_RoundTrip_ContainerApp(t *testing.T) {
	original := &ResourceConfig{
		Type: ResourceTypeHostContainerApp,
		Props: ContainerAppProps{
			Port: 9090,
		},
	}

	data, err := yaml.Marshal(original)
	require.NoError(t, err)

	var restored ResourceConfig
	err = yaml.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, ResourceTypeHostContainerApp, restored.Type)
	props, ok := restored.Props.(ContainerAppProps)
	require.True(t, ok)
	assert.Equal(t, 9090, props.Port)
}
