// Copyright (c) Microsoft Corporation. Licensed under the MIT License.
package project

import (
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- mapHostUses: non-existing resource switch cases ----

func Test_mapHostUses_NonExistingResources_Coverage3(t *testing.T) {
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

func Test_mapHostUses_MissingResource_Coverage3(t *testing.T) {
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

func Test_mapHostUses_BackendMapping_Coverage3(t *testing.T) {
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

// ---- mapContainerApp ----

func Test_mapContainerApp_Coverage3(t *testing.T) {
	res := &ResourceConfig{
		Name:  "myapp",
		Props: ContainerAppProps{Port: 8080},
	}
	svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
	infraSpec := &scaffold.InfraSpec{}

	err := mapContainerApp(res, svcSpec, infraSpec)
	require.NoError(t, err)
	assert.Equal(t, 8080, svcSpec.Port)
}

func Test_mapContainerApp_WithEnv_Coverage3(t *testing.T) {
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

// ---- OverriddenEndpoints ----

func Test_OverriddenEndpoints_Coverage3(t *testing.T) {
	t.Run("NoOverride", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{})
		sc := &ServiceConfig{Name: "api"}

		endpoints := OverriddenEndpoints(t.Context(), sc, env)
		assert.Nil(t, endpoints)
	})

	t.Run("ValidJSON", func(t *testing.T) {
		urls := []string{"https://app.azurewebsites.net", "https://app-slot.azurewebsites.net"}
		jsonBytes, _ := json.Marshal(urls)
		env := environment.NewWithValues("test", map[string]string{
			"SERVICE_API_ENDPOINTS": string(jsonBytes),
		})
		sc := &ServiceConfig{Name: "api"}

		endpoints := OverriddenEndpoints(t.Context(), sc, env)
		assert.Equal(t, urls, endpoints)
	})

	t.Run("InvalidJSON_returns_nil", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			"SERVICE_API_ENDPOINTS": "not-json",
		})
		sc := &ServiceConfig{Name: "api"}

		endpoints := OverriddenEndpoints(t.Context(), sc, env)
		assert.Nil(t, endpoints)
	})
}
