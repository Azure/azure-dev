package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_mapHostUses_ExistingResource_Coverage3(t *testing.T) {
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
