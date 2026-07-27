// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

func Test_AllResourceTypes(t *testing.T) {
	all := AllResourceTypes()

	require.NotEmpty(t, all)

	// Verify every known constant is present
	expected := []ResourceType{
		ResourceTypeDbRedis,
		ResourceTypeDbPostgres,
		ResourceTypeDbMySql,
		ResourceTypeDbMongo,
		ResourceTypeDbCosmos,
		ResourceTypeHostAppService,
		ResourceTypeHostContainerApp,
		ResourceTypeOpenAiModel,
		ResourceTypeMessagingEventHubs,
		ResourceTypeMessagingServiceBus,
		ResourceTypeStorage,
		ResourceTypeAiProject,
		ResourceTypeAiSearch,
		ResourceTypeKeyVault,
	}

	require.Equal(t, expected, all)
}

func Test_ResourceType_String(t *testing.T) {
	tests := []struct {
		name     string
		rt       ResourceType
		expected string
	}{
		{"Redis", ResourceTypeDbRedis, "Redis"},
		{"PostgreSQL", ResourceTypeDbPostgres, "PostgreSQL"},
		{"MySQL", ResourceTypeDbMySql, "MySQL"},
		{"MongoDB", ResourceTypeDbMongo, "MongoDB"},
		{"CosmosDB", ResourceTypeDbCosmos, "CosmosDB"},
		{
			"App Service",
			ResourceTypeHostAppService,
			"App Service",
		},
		{
			"Container App",
			ResourceTypeHostContainerApp,
			"Container App",
		},
		{
			"Open AI Model",
			ResourceTypeOpenAiModel,
			"Open AI Model",
		},
		{
			"Event Hubs",
			ResourceTypeMessagingEventHubs,
			"Event Hubs",
		},
		{
			"Service Bus",
			ResourceTypeMessagingServiceBus,
			"Service Bus",
		},
		{
			"Storage Account",
			ResourceTypeStorage,
			"Storage Account",
		},
		{"Foundry", ResourceTypeAiProject, "Foundry"},
		{"AI Search", ResourceTypeAiSearch, "AI Search"},
		{"Key Vault", ResourceTypeKeyVault, "Key Vault"},
		{
			"unknown returns empty",
			ResourceType("unknown.type"),
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.rt.String())
		})
	}
}

func Test_ResourceType_AzureResourceType(t *testing.T) {
	tests := []struct {
		name     string
		rt       ResourceType
		expected string
	}{
		{
			"AppService",
			ResourceTypeHostAppService,
			"Microsoft.Web/sites",
		},
		{
			"ContainerApp",
			ResourceTypeHostContainerApp,
			"Microsoft.App/containerApps",
		},
		{
			"Redis",
			ResourceTypeDbRedis,
			"Microsoft.Cache/redis",
		},
		{
			"Postgres",
			ResourceTypeDbPostgres,
			"Microsoft.DBforPostgreSQL/flexibleServers/databases",
		},
		{
			"MySQL",
			ResourceTypeDbMySql,
			"Microsoft.DBforMySQL/flexibleServers/databases",
		},
		{
			"MongoDB",
			ResourceTypeDbMongo,
			"Microsoft.DocumentDB/databaseAccounts/mongodbDatabases",
		},
		{
			"OpenAI Model",
			ResourceTypeOpenAiModel,
			"Microsoft.CognitiveServices/accounts/deployments",
		},
		{
			"CosmosDB",
			ResourceTypeDbCosmos,
			"Microsoft.DocumentDB/databaseAccounts/sqlDatabases",
		},
		{
			"EventHubs",
			ResourceTypeMessagingEventHubs,
			"Microsoft.EventHub/namespaces",
		},
		{
			"ServiceBus",
			ResourceTypeMessagingServiceBus,
			"Microsoft.ServiceBus/namespaces",
		},
		{
			"Storage",
			ResourceTypeStorage,
			"Microsoft.Storage/storageAccounts",
		},
		{
			"KeyVault",
			ResourceTypeKeyVault,
			"Microsoft.KeyVault/vaults",
		},
		{
			"AiProject",
			ResourceTypeAiProject,
			"Microsoft.CognitiveServices/accounts/projects",
		},
		{
			"AiSearch",
			ResourceTypeAiSearch,
			"Microsoft.Search/searchServices",
		},
		{
			"unknown returns empty",
			ResourceType("custom.thing"),
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.rt.AzureResourceType())
		})
	}
}

func Test_AllResourceTypes_StringAndAzureType_Complete(
	t *testing.T,
) {
	// Every type in AllResourceTypes should have a non-empty
	// String() and AzureResourceType() value.
	for _, rt := range AllResourceTypes() {
		t.Run(string(rt), func(t *testing.T) {
			require.NotEmpty(
				t,
				rt.String(),
				"String() should not be empty for %s",
				rt,
			)
			require.NotEmpty(
				t,
				rt.AzureResourceType(),
				"AzureResourceType() should not be empty for %s",
				rt,
			)
		})
	}
}

// Verify minimum count of resource types

// Check completeness

// Focus on edge case: unknown type returns empty string

// Focus on edge case: unknown type returns empty string

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

// expandableStringTemplate extracts the template string from an ExpandableString
// by converting it to string via its MarshalYAML/String representation.
func expandableStringTemplate(es osutil.ExpandableString) string {
	// ExpandableString.MarshalYAML returns the template string
	v, _ := es.MarshalYAML()
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func Test_infraSpec_DbRedis(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"cache": {Name: "cache", Type: ResourceTypeDbRedis},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.DbRedis)
	// Redis also adds an implicit KeyVault dependency via DependentResourcesOf
	require.NotNil(t, spec.KeyVault)
}

func Test_infraSpec_DbMongo(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"mongo": {Name: "mongo", Type: ResourceTypeDbMongo},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.DbCosmosMongo)
	assert.Equal(t, "mongo", spec.DbCosmosMongo.DatabaseName)
}

func Test_infraSpec_DbCosmos(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"cosmos": {
				Name: "cosmos",
				Type: ResourceTypeDbCosmos,
				Props: CosmosDBProps{
					Containers: []CosmosDBContainerProps{
						{Name: "items", PartitionKeys: []string{"/id"}},
					},
				},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.DbCosmos)
	assert.Equal(t, "cosmos", spec.DbCosmos.DatabaseName)
	require.Len(t, spec.DbCosmos.Containers, 1)
	assert.Equal(t, "items", spec.DbCosmos.Containers[0].ContainerName)
}

func Test_infraSpec_DbPostgres(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"pg": {Name: "pg", Type: ResourceTypeDbPostgres},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.DbPostgres)
	assert.Equal(t, "pg", spec.DbPostgres.DatabaseName)
}

func Test_infraSpec_DbMySql(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"mysql": {Name: "mysql", Type: ResourceTypeDbMySql},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.DbMySql)
	assert.Equal(t, "mysql", spec.DbMySql.DatabaseName)
}

func Test_infraSpec_OpenAiModel_MissingName(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"model": {
				Name:  "model",
				Type:  ResourceTypeOpenAiModel,
				Props: AIModelProps{Model: AIModelPropsModel{Version: "v1"}},
			},
		},
	}
	_, err := infraSpec(prj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model is required")
}

func Test_infraSpec_OpenAiModel_MissingVersion(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"model": {
				Name:  "model",
				Type:  ResourceTypeOpenAiModel,
				Props: AIModelProps{Model: AIModelPropsModel{Name: "gpt-4"}},
			},
		},
	}
	_, err := infraSpec(prj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version is required")
}

func Test_infraSpec_EventHubs(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"eh": {
				Name:  "eh",
				Type:  ResourceTypeMessagingEventHubs,
				Props: EventHubsProps{Hubs: []string{"hub1", "hub2"}},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.EventHubs)
	assert.Equal(t, []string{"hub1", "hub2"}, spec.EventHubs.Hubs)
}

func Test_infraSpec_EventHubs_Duplicate(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"eh1": {Name: "eh1", Type: ResourceTypeMessagingEventHubs, Props: EventHubsProps{}},
			"eh2": {Name: "eh2", Type: ResourceTypeMessagingEventHubs, Props: EventHubsProps{}},
		},
	}
	_, err := infraSpec(prj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one event hubs")
}

func Test_infraSpec_ServiceBus(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"sb": {
				Name:  "sb",
				Type:  ResourceTypeMessagingServiceBus,
				Props: ServiceBusProps{Queues: []string{"q1"}, Topics: []string{"t1"}},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.ServiceBus)
	assert.Equal(t, []string{"q1"}, spec.ServiceBus.Queues)
	assert.Equal(t, []string{"t1"}, spec.ServiceBus.Topics)
}

func Test_infraSpec_ServiceBus_Duplicate(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"sb1": {Name: "sb1", Type: ResourceTypeMessagingServiceBus, Props: ServiceBusProps{}},
			"sb2": {Name: "sb2", Type: ResourceTypeMessagingServiceBus, Props: ServiceBusProps{}},
		},
	}
	_, err := infraSpec(prj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one service bus")
}

func Test_infraSpec_Storage(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"store": {
				Name:  "store",
				Type:  ResourceTypeStorage,
				Props: StorageProps{Containers: []string{"blobs", "data"}},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.StorageAccount)
	assert.Equal(t, []string{"blobs", "data"}, spec.StorageAccount.Containers)
}

func Test_infraSpec_Storage_Duplicate(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"s1": {Name: "s1", Type: ResourceTypeStorage, Props: StorageProps{}},
			"s2": {Name: "s2", Type: ResourceTypeStorage, Props: StorageProps{}},
		},
	}
	_, err := infraSpec(prj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one storage account")
}

func Test_infraSpec_KeyVault(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"vault": {Name: "vault", Type: ResourceTypeKeyVault},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.KeyVault)
}

func Test_infraSpec_AiSearch(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"search": {Name: "search", Type: ResourceTypeAiSearch},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.AISearch)
}

func Test_infraSpec_HostAppService_MissingSvc(t *testing.T) {
	prj := &ProjectConfig{
		Services: map[string]*ServiceConfig{},
		Resources: map[string]*ResourceConfig{
			"app": {
				Name:  "app",
				Type:  ResourceTypeHostAppService,
				Props: AppServiceProps{},
			},
		},
	}
	_, err := infraSpec(prj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service app not found")
}

func Test_infraSpec_EmptyResources(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	assert.Empty(t, spec.Services)
}

func Test_infraSpec_MultipleResourceTypes(t *testing.T) {
	// Tests multiple resource types together
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"cache":  {Name: "cache", Type: ResourceTypeDbRedis},
			"pg":     {Name: "pg", Type: ResourceTypeDbPostgres},
			"search": {Name: "search", Type: ResourceTypeAiSearch},
			"vault":  {Name: "vault", Type: ResourceTypeKeyVault},
			"store":  {Name: "store", Type: ResourceTypeStorage, Props: StorageProps{Containers: []string{"data"}}},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	assert.NotNil(t, spec.DbRedis)
	assert.NotNil(t, spec.DbPostgres)
	assert.NotNil(t, spec.AISearch)
	assert.NotNil(t, spec.KeyVault)
	assert.NotNil(t, spec.StorageAccount)
}

func Test_DependentResourcesOf(t *testing.T) {
	tests := []struct {
		name    string
		resType ResourceType
		hasDeps bool
		depType ResourceType
	}{
		{"Mongo", ResourceTypeDbMongo, true, ResourceTypeKeyVault},
		{"MySql", ResourceTypeDbMySql, true, ResourceTypeKeyVault},
		{"Postgres", ResourceTypeDbPostgres, true, ResourceTypeKeyVault},
		{"Redis", ResourceTypeDbRedis, true, ResourceTypeKeyVault},
		{"AppService", ResourceTypeHostAppService, false, ""},
		{"ContainerApp", ResourceTypeHostContainerApp, false, ""},
		{"Storage", ResourceTypeStorage, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &ResourceConfig{Name: "test", Type: tt.resType}
			deps := DependentResourcesOf(res)
			if tt.hasDeps {
				require.NotEmpty(t, deps)
				assert.Equal(t, tt.depType, deps[0].Type)
			} else {
				assert.Empty(t, deps)
			}
		})
	}
}

func Test_mapAppService(t *testing.T) {
	t.Run("MissingRuntimeStack", func(t *testing.T) {
		res := &ResourceConfig{
			Name: "web",
			Props: AppServiceProps{
				Runtime: AppServiceRuntime{
					Stack:   "",
					Version: "3.11",
				},
			},
		}
		svcSpec := &scaffold.ServiceSpec{}
		infraSpec := &scaffold.InfraSpec{}
		svcConfig := &ServiceConfig{}

		err := mapAppService(res, svcSpec, infraSpec, svcConfig)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "runtime.type is required")
	})

	t.Run("MissingRuntimeVersion", func(t *testing.T) {
		res := &ResourceConfig{
			Name: "web",
			Props: AppServiceProps{
				Runtime: AppServiceRuntime{
					Stack:   "python",
					Version: "",
				},
			},
		}
		svcSpec := &scaffold.ServiceSpec{}
		infraSpec := &scaffold.InfraSpec{}
		svcConfig := &ServiceConfig{}

		err := mapAppService(res, svcSpec, infraSpec, svcConfig)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "runtime.version is required")
	})

	t.Run("ValidPythonService", func(t *testing.T) {
		res := &ResourceConfig{
			Name: "web",
			Props: AppServiceProps{
				Runtime: AppServiceRuntime{
					Stack:   "python",
					Version: "3.11",
				},
				Port:           8080,
				StartupCommand: "gunicorn app:app",
				Env: []ServiceEnvVar{
					{Name: "APP_ENV", Value: "production"},
				},
			},
		}
		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		infraSpec := &scaffold.InfraSpec{}
		svcConfig := &ServiceConfig{Language: ServiceLanguagePython}

		err := mapAppService(res, svcSpec, infraSpec, svcConfig)
		require.NoError(t, err)
		require.NotNil(t, svcSpec.Runtime)
		assert.Equal(t, "python", svcSpec.Runtime.Type)
		assert.Equal(t, "3.11", svcSpec.Runtime.Version)
		assert.Equal(t, "gunicorn app:app", svcSpec.StartupCommand)
		assert.Equal(t, 8080, svcSpec.Port)
	})

	t.Run("ValidNodeService", func(t *testing.T) {
		res := &ResourceConfig{
			Name: "api",
			Props: AppServiceProps{
				Runtime: AppServiceRuntime{
					Stack:   "node",
					Version: "18-lts",
				},
				Port: 3000,
				Env: []ServiceEnvVar{
					{Name: "NODE_ENV", Value: "production"},
				},
			},
		}
		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		infraSpec := &scaffold.InfraSpec{}
		svcConfig := &ServiceConfig{Language: ServiceLanguageJavaScript}

		err := mapAppService(res, svcSpec, infraSpec, svcConfig)
		require.NoError(t, err)
		require.NotNil(t, svcSpec.Runtime)
		assert.Equal(t, "node", svcSpec.Runtime.Type)
		assert.Equal(t, "18-lts", svcSpec.Runtime.Version)
		assert.Equal(t, 3000, svcSpec.Port)
	})

	t.Run("NoEnvVars", func(t *testing.T) {
		res := &ResourceConfig{
			Name: "simple",
			Props: AppServiceProps{
				Runtime: AppServiceRuntime{
					Stack:   "dotnet",
					Version: "8.0",
				},
				Port: 80,
			},
		}
		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		infraSpec := &scaffold.InfraSpec{}
		svcConfig := &ServiceConfig{}

		err := mapAppService(res, svcSpec, infraSpec, svcConfig)
		require.NoError(t, err)
		assert.Equal(t, "dotnet", svcSpec.Runtime.Type)
		assert.Equal(t, 80, svcSpec.Port)
	})
}

func Test_mapHostUses_NonExistingResources(t *testing.T) {
	// Each sub-test verifies one switch-case branch in mapHostUses for non-existing resources.
	tests := []struct {
		name     string
		useType  ResourceType
		validate func(t *testing.T, svcSpec *scaffold.ServiceSpec)
	}{
		{
			"DbMongo",
			ResourceTypeDbMongo,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.DbCosmosMongo)
				assert.Equal(t, "dep1", s.DbCosmosMongo.DatabaseName)
			},
		},
		{
			"DbCosmos",
			ResourceTypeDbCosmos,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.DbCosmos)
				assert.Equal(t, "dep1", s.DbCosmos.DatabaseName)
			},
		},
		{
			"DbPostgres",
			ResourceTypeDbPostgres,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.DbPostgres)
				assert.Equal(t, "dep1", s.DbPostgres.DatabaseName)
			},
		},
		{
			"DbMySql",
			ResourceTypeDbMySql,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.DbMySql)
				assert.Equal(t, "dep1", s.DbMySql.DatabaseName)
			},
		},
		{
			"DbRedis",
			ResourceTypeDbRedis,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.DbRedis)
				assert.Equal(t, "dep1", s.DbRedis.DatabaseName)
			},
		},
		{
			"HostAppService_creates_frontend",
			ResourceTypeHostAppService,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.Frontend)
				require.Len(t, s.Frontend.Backends, 1)
				assert.Equal(t, "dep1", s.Frontend.Backends[0].Name)
			},
		},
		{
			"HostContainerApp_creates_frontend",
			ResourceTypeHostContainerApp,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.Frontend)
				require.Len(t, s.Frontend.Backends, 1)
				assert.Equal(t, "dep1", s.Frontend.Backends[0].Name)
			},
		},
		{
			"OpenAiModel",
			ResourceTypeOpenAiModel,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.Len(t, s.AIModels, 1)
				assert.Equal(t, "dep1", s.AIModels[0].Name)
			},
		},
		{
			"EventHubs",
			ResourceTypeMessagingEventHubs,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.EventHubs)
			},
		},
		{
			"ServiceBus",
			ResourceTypeMessagingServiceBus,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.ServiceBus)
			},
		},
		{
			"Storage",
			ResourceTypeStorage,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.StorageAccount)
			},
		},
		{
			"AiProject",
			ResourceTypeAiProject,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.AiFoundryProject)
			},
		},
		{
			"AiSearch",
			ResourceTypeAiSearch,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.AISearch)
			},
		},
		{
			"KeyVault",
			ResourceTypeKeyVault,
			func(t *testing.T, s *scaffold.ServiceSpec) {
				require.NotNil(t, s.KeyVault)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prj := &ProjectConfig{
				Resources: map[string]*ResourceConfig{
					"dep1": {Name: "dep1", Type: tt.useType},
				},
			}
			res := &ResourceConfig{
				Name: "web",
				Uses: []string{"dep1"},
			}
			svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
			backendMapping := map[string]string{}
			existingMap := map[string]*scaffold.ExistingResource{}

			err := mapHostUses(res, svcSpec, backendMapping, existingMap, prj)
			require.NoError(t, err)
			tt.validate(t, svcSpec)
		})
	}
}

func Test_mapHostUses_MissingResource(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{},
	}
	res := &ResourceConfig{
		Name: "web",
		Uses: []string{"nonexistent"},
	}
	svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}

	err := mapHostUses(res, svcSpec, map[string]string{}, map[string]*scaffold.ExistingResource{}, prj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func Test_mapHostUses_BackendMapping(t *testing.T) {
	// Verifies that the backendMapping is populated for host resources
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"api": {Name: "api", Type: ResourceTypeHostContainerApp},
		},
	}
	res := &ResourceConfig{
		Name: "frontend",
		Uses: []string{"api"},
	}
	svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
	backendMapping := map[string]string{}

	err := mapHostUses(res, svcSpec, backendMapping, map[string]*scaffold.ExistingResource{}, prj)
	require.NoError(t, err)
	assert.Equal(t, "frontend", backendMapping["api"])
}

func Test_mapContainerApp_WithEnv(t *testing.T) {
	res := &ResourceConfig{
		Name: "myapp",
		Props: ContainerAppProps{
			Port: 3000,
			Env:  []ServiceEnvVar{{Name: "KEY", Value: "val"}},
		},
	}
	svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
	infraSpec := &scaffold.InfraSpec{}

	err := mapContainerApp(res, svcSpec, infraSpec)
	require.NoError(t, err)
	assert.Equal(t, 3000, svcSpec.Port)
}

func Test_mapHostUses_ExistingResource(t *testing.T) {
	t.Run("SupportedExistingType_Redis_VaultExprError", func(t *testing.T) {
		prj := &ProjectConfig{
			Resources: map[string]*ResourceConfig{
				"myapp": {
					Name: "myapp",
					Type: ResourceTypeHostContainerApp,
					Uses: []string{"myredis"},
				},
				"myredis": {
					Name:     "myredis",
					Type:     ResourceTypeDbRedis,
					Existing: true,
				},
			},
		}

		existingMap := map[string]*scaffold.ExistingResource{
			"myredis": {
				Name:         "existingRedis",
				ResourceType: "Microsoft.Cache/redis",
				ApiVersion:   "2024-03-01",
			},
		}

		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		backendMapping := map[string]string{}
		err := mapHostUses(prj.Resources["myapp"], svcSpec, backendMapping, existingMap, prj)
		// Redis has vault expressions which are not supported for existing resources
		require.Error(t, err)
		assert.Contains(t, err.Error(), "vault expressions are not currently supported")
	})

	t.Run("UnsupportedExistingType_Error", func(t *testing.T) {
		prj := &ProjectConfig{
			Resources: map[string]*ResourceConfig{
				"myapp": {
					Name: "myapp",
					Type: ResourceTypeHostContainerApp,
					Uses: []string{"mywebapp"},
				},
				"mywebapp": {
					Name:     "mywebapp",
					Type:     ResourceTypeHostAppService,
					Existing: true,
				},
			},
		}

		existingMap := map[string]*scaffold.ExistingResource{}

		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		err := mapHostUses(prj.Resources["myapp"], svcSpec, map[string]string{}, existingMap, prj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not currently supported for existing")
	})

	t.Run("ExistingPostgres_SpecExprError", func(t *testing.T) {
		prj := &ProjectConfig{
			Resources: map[string]*ResourceConfig{
				"myapp": {
					Name: "myapp",
					Type: ResourceTypeHostContainerApp,
					Uses: []string{"mydb"},
				},
				"mydb": {
					Name:     "mydb",
					Type:     ResourceTypeDbPostgres,
					Existing: true,
				},
			},
		}

		existingMap := map[string]*scaffold.ExistingResource{
			"mydb": {
				Name:         "existingPostgres",
				ResourceType: "Microsoft.DBforPostgreSQL/flexibleServers/databases",
				ApiVersion:   "2022-12-01",
			},
		}

		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		err := mapHostUses(prj.Resources["myapp"], svcSpec, map[string]string{}, existingMap, prj)
		// Postgres has spec expressions which are not supported for existing resources
		require.Error(t, err)
		assert.Contains(t, err.Error(), "spec expressions are not currently supported")
	})

	t.Run("ExistingCosmos", func(t *testing.T) {
		prj := &ProjectConfig{
			Resources: map[string]*ResourceConfig{
				"myapp": {
					Name: "myapp",
					Type: ResourceTypeHostContainerApp,
					Uses: []string{"mycosmos"},
				},
				"mycosmos": {
					Name:     "mycosmos",
					Type:     ResourceTypeDbCosmos,
					Existing: true,
				},
			},
		}

		existingMap := map[string]*scaffold.ExistingResource{
			"mycosmos": {
				Name:         "existingCosmos",
				ResourceType: "Microsoft.DocumentDB/databaseAccounts/sqlDatabases",
				ApiVersion:   "2023-04-15",
			},
		}

		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		err := mapHostUses(prj.Resources["myapp"], svcSpec, map[string]string{}, existingMap, prj)
		require.NoError(t, err)
		require.Len(t, svcSpec.Existing, 1)
		assert.Equal(t, "existingCosmos", svcSpec.Existing[0].Name)
	})

	t.Run("ExistingKeyVault", func(t *testing.T) {
		prj := &ProjectConfig{
			Resources: map[string]*ResourceConfig{
				"myapp": {
					Name: "myapp",
					Type: ResourceTypeHostContainerApp,
					Uses: []string{"mykv"},
				},
				"mykv": {
					Name:     "mykv",
					Type:     ResourceTypeKeyVault,
					Existing: true,
				},
			},
		}

		existingMap := map[string]*scaffold.ExistingResource{
			"mykv": {
				Name:         "existingKv",
				ResourceType: "Microsoft.KeyVault/vaults",
				ApiVersion:   "2023-07-01",
			},
		}

		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		err := mapHostUses(prj.Resources["myapp"], svcSpec, map[string]string{}, existingMap, prj)
		require.NoError(t, err)
		require.Len(t, svcSpec.Existing, 1)
		assert.Equal(t, "existingKv", svcSpec.Existing[0].Name)
	})

	t.Run("ExistingStorage", func(t *testing.T) {
		prj := &ProjectConfig{
			Resources: map[string]*ResourceConfig{
				"myapp": {
					Name: "myapp",
					Type: ResourceTypeHostContainerApp,
					Uses: []string{"mystorage"},
				},
				"mystorage": {
					Name:     "mystorage",
					Type:     ResourceTypeStorage,
					Existing: true,
				},
			},
		}

		existingMap := map[string]*scaffold.ExistingResource{
			"mystorage": {
				Name:         "existingStorage",
				ResourceType: "Microsoft.Storage/storageAccounts",
				ApiVersion:   "2023-01-01",
			},
		}

		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		err := mapHostUses(prj.Resources["myapp"], svcSpec, map[string]string{}, existingMap, prj)
		require.NoError(t, err)
		require.Len(t, svcSpec.Existing, 1)
		assert.Equal(t, "existingStorage", svcSpec.Existing[0].Name)
	})
}
