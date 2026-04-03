package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- infraSpec: drives each resource-type switch case ----

func Test_infraSpec_DbRedis_Coverage3(t *testing.T) {
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

func Test_infraSpec_DbMongo_Coverage3(t *testing.T) {
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

func Test_infraSpec_DbCosmos_Coverage3(t *testing.T) {
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

func Test_infraSpec_DbPostgres_Coverage3(t *testing.T) {
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

func Test_infraSpec_DbMySql_Coverage3(t *testing.T) {
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

func Test_infraSpec_OpenAiModel_Coverage3(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"gpt4": {
				Name: "gpt4",
				Type: ResourceTypeOpenAiModel,
				Props: AIModelProps{
					Model: AIModelPropsModel{Name: "gpt-4", Version: "0613"},
				},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.Len(t, spec.AIModels, 1)
	assert.Equal(t, "gpt4", spec.AIModels[0].Name)
	assert.Equal(t, "gpt-4", spec.AIModels[0].Model.Name)
	assert.Equal(t, "0613", spec.AIModels[0].Model.Version)
}

func Test_infraSpec_OpenAiModel_MissingName_Coverage3(t *testing.T) {
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

func Test_infraSpec_OpenAiModel_MissingVersion_Coverage3(t *testing.T) {
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

func Test_infraSpec_EventHubs_Coverage3(t *testing.T) {
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

func Test_infraSpec_EventHubs_Duplicate_Coverage3(t *testing.T) {
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

func Test_infraSpec_ServiceBus_Coverage3(t *testing.T) {
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

func Test_infraSpec_ServiceBus_Duplicate_Coverage3(t *testing.T) {
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

func Test_infraSpec_Storage_Coverage3(t *testing.T) {
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

func Test_infraSpec_Storage_Duplicate_Coverage3(t *testing.T) {
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

func Test_infraSpec_AiProject_Coverage3(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"foundry": {
				Name: "foundry",
				Type: ResourceTypeAiProject,
				Props: AiFoundryModelProps{
					Models: []AiServicesModel{
						{
							Name:    "gpt-4o",
							Version: "2024-05-13",
							Format:  "OpenAI",
							Sku: AiServicesModelSku{
								Name:      "Standard",
								UsageName: "standard",
								Capacity:  10,
							},
						},
					},
				},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.AiFoundryProject)
	assert.Equal(t, "foundry", spec.AiFoundryProject.Name)
	require.Len(t, spec.AiFoundryProject.Models, 1)
	assert.Equal(t, "gpt-4o", spec.AiFoundryProject.Models[0].Name)
}

func Test_infraSpec_KeyVault_Coverage3(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"vault": {Name: "vault", Type: ResourceTypeKeyVault},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.KeyVault)
}

func Test_infraSpec_AiSearch_Coverage3(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"search": {Name: "search", Type: ResourceTypeAiSearch},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.AISearch)
}

func Test_infraSpec_HostContainerApp_Coverage3(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"api": {
				Name:  "api",
				Type:  ResourceTypeHostContainerApp,
				Props: ContainerAppProps{Port: 8080},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.Len(t, spec.Services, 1)
	assert.Equal(t, "api", spec.Services[0].Name)
	assert.Equal(t, 8080, spec.Services[0].Port)
	assert.Equal(t, scaffold.ContainerAppKind, spec.Services[0].Host)
}

func Test_infraSpec_HostContainerApp_WithDeps_Coverage3(t *testing.T) {
	// Tests backend-frontend mapping reverse pass
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"api": {
				Name:  "api",
				Type:  ResourceTypeHostContainerApp,
				Props: ContainerAppProps{Port: 3000},
			},
			"web": {
				Name:  "web",
				Type:  ResourceTypeHostContainerApp,
				Props: ContainerAppProps{Port: 8080},
				Uses:  []string{"api"},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.Len(t, spec.Services, 2)

	// Services are sorted by name
	assert.Equal(t, "api", spec.Services[0].Name)
	assert.Equal(t, "web", spec.Services[1].Name)

	// api should have a Backend pointing to frontend "web"
	require.NotNil(t, spec.Services[0].Backend)
	require.Len(t, spec.Services[0].Backend.Frontends, 1)
	assert.Equal(t, "web", spec.Services[0].Backend.Frontends[0].Name)

	// web should have a Frontend pointing to backend "api"
	require.NotNil(t, spec.Services[1].Frontend)
	require.Len(t, spec.Services[1].Frontend.Backends, 1)
	assert.Equal(t, "api", spec.Services[1].Frontend.Backends[0].Name)
}

func Test_infraSpec_HostAppService_MissingSvc_Coverage3(t *testing.T) {
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

func Test_infraSpec_HostAppService_Valid_Coverage3(t *testing.T) {
	dir := t.TempDir()
	prj := &ProjectConfig{
		Path: dir,
		Services: map[string]*ServiceConfig{
			"webapp": {
				Name:         "webapp",
				Language:     ServiceLanguagePython,
				RelativePath: ".",
				Project:      nil, // will be set below
			},
		},
		Resources: map[string]*ResourceConfig{
			"webapp": {
				Name: "webapp",
				Type: ResourceTypeHostAppService,
				Props: AppServiceProps{
					Port:    8000,
					Runtime: AppServiceRuntime{Stack: "python", Version: "3.12"},
				},
			},
		},
	}
	prj.Services["webapp"].Project = prj

	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.Len(t, spec.Services, 1)
	assert.Equal(t, "webapp", spec.Services[0].Name)
	assert.Equal(t, scaffold.AppServiceKind, spec.Services[0].Host)
	assert.Equal(t, 8000, spec.Services[0].Port)
}

func Test_infraSpec_EmptyResources_Coverage3(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	assert.Empty(t, spec.Services)
}

func Test_infraSpec_MultipleResourceTypes_Coverage3(t *testing.T) {
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

// ---- DependentResourcesOf ----

func Test_DependentResourcesOf_Coverage3(t *testing.T) {
	tests := []struct {
		name     string
		resType  ResourceType
		hasDeps  bool
		depType  ResourceType
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

// ---- artifact.go ToString ----

func Test_ArtifactToString_Coverage3(t *testing.T) {
	tests := []struct {
		name     string
		artifact Artifact
		contains string
	}{
		{
			"Endpoint_remote",
			Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://app.azurewebsites.net",
				LocationKind: LocationKindRemote,
			},
			"https://app.azurewebsites.net",
		},
		{
			"Container_remote",
			Artifact{
				Kind:         ArtifactKindContainer,
				Location:     "myregistry.azurecr.io/app:latest",
				LocationKind: LocationKindRemote,
			},
			"Remote Image",
		},
		{
			"Container_local",
			Artifact{
				Kind:         ArtifactKindContainer,
				Location:     "app:latest",
				LocationKind: LocationKindLocal,
			},
			"Container",
		},
		{
			"Archive",
			Artifact{
				Kind:         ArtifactKindArchive,
				Location:     "/tmp/app.zip",
				LocationKind: LocationKindLocal,
			},
			"Package Output",
		},
		{
			"Directory",
			Artifact{
				Kind:         ArtifactKindDirectory,
				Location:     "/tmp/output",
				LocationKind: LocationKindLocal,
			},
			"Build Output",
		},
		{
			"Unknown",
			Artifact{
				Kind:         ArtifactKind("unknown"),
				Location:     "test",
				LocationKind: LocationKindLocal,
			},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.artifact.ToString("")
			if tt.contains != "" {
				assert.Contains(t, result, tt.contains)
			} else {
				assert.Equal(t, "", result)
			}
		})
	}
}

func Test_ArtifactToString_Endpoint_WithNote_Coverage3(t *testing.T) {
	a := Artifact{
		Kind:         ArtifactKindEndpoint,
		Location:     "https://example.com",
		LocationKind: LocationKindRemote,
		Metadata:     map[string]string{MetadataKeyNote: "Primary endpoint"},
	}
	result := a.ToString("")
	assert.Contains(t, result, "https://example.com")
	assert.Contains(t, result, "Primary endpoint")
}
