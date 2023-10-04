package devcenter

import (
	"context"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockdevcentersdk"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var mockEnvironments []*devcentersdk.Environment = []*devcentersdk.Environment{
	{
		Name:                      "user01-project1-dev-01",
		CatalogName:               "SampleCatalog",
		EnvironmentDefinitionName: "WebApp",
		EnvironmentType:           "Dev",
	},
	{
		Name:                      "user01-project1-dev-02",
		CatalogName:               "SampleCatalog",
		EnvironmentDefinitionName: "WebApp",
		EnvironmentType:           "Dev",
	},
	{
		Name:                      "user01-project1-dev-03",
		CatalogName:               "SampleCatalog",
		EnvironmentDefinitionName: "ContainerApp",
		EnvironmentType:           "Dev",
	},
}

func Test_EnvironmentStore_List(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
	mockdevcentersdk.MockListEnvironments(mockContext, "Project1", mockEnvironments)

	t.Run("AllMatchingEnvironments", func(t *testing.T) {
		config := &Config{
			Name:                  "DEV_CENTER",
			Project:               "Project1",
			EnvironmentDefinition: "WebApp",
			Catalog:               "SampleCatalog",
			EnvironmentType:       "Dev",
		}

		store := newEnvironmentStoreForTest(t, mockContext, config, nil)
		envList, err := store.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
		require.Len(t, envList, 2)
	})

	t.Run("SomeMatchingEnvironments", func(t *testing.T) {
		config := &Config{
			Name:                  "DEV_CENTER",
			Project:               "Project1",
			EnvironmentDefinition: "ContainerApp",
			Catalog:               "SampleCatalog",
			EnvironmentType:       "Dev",
		}

		store := newEnvironmentStoreForTest(t, mockContext, config, nil)
		envList, err := store.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
		require.Len(t, envList, 1)
	})

	t.Run("NoMatchingEnvironments", func(t *testing.T) {
		config := &Config{
			Name:                  "DEV_CENTER",
			Project:               "Project1",
			EnvironmentDefinition: "FunctionApp",
			Catalog:               "SampleCatalog",
			EnvironmentType:       "Dev",
		}

		store := newEnvironmentStoreForTest(t, mockContext, config, nil)
		envList, err := store.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
		require.Len(t, envList, 0)
	})
}

func Test_EnvironmentStore_Get(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
	mockdevcentersdk.MockListEnvironments(mockContext, "Project1", mockEnvironments)

	t.Run("Exists", func(t *testing.T) {
		mockdevcentersdk.MockGetEnvironment(mockContext, "Project1", "me", mockEnvironments[0].Name, mockEnvironments[0])

		config := &Config{
			Name:                  "DEV_CENTER",
			Project:               "Project1",
			EnvironmentDefinition: "WebApp",
			Catalog:               "SampleCatalog",
			EnvironmentType:       "Dev",
		}

		outputs := map[string]provisioning.OutputParameter{
			"KEY1": {
				Type:  "string",
				Value: "value1",
			},
			"KEY2": {
				Type:  "string",
				Value: "value2",
			},
		}

		manager := &mockDevCenterManager{}
		manager.
			On("Outputs", *mockContext.Context, mock.AnythingOfType("*devcentersdk.Environment")).
			Return(outputs, nil)

		store := newEnvironmentStoreForTest(t, mockContext, config, manager)
		env, err := store.Get(*mockContext.Context, mockEnvironments[0].Name)
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, mockEnvironments[0].Name, env.GetEnvName())
		require.Equal(t, "value1", env.Getenv("KEY1"))
		require.Equal(t, "value2", env.Getenv("KEY2"))

		devCenterNode, ok := env.Config.Get("devCenter")
		require.True(t, ok)

		envConfig, err := ParseConfig(devCenterNode)
		require.NoError(t, err)
		require.Equal(t, *envConfig, *config)
	})

	t.Run("DoesNotExist", func(t *testing.T) {
		config := &Config{
			Name:                  "DEV_CENTER",
			Project:               "Project1",
			EnvironmentDefinition: "WebApp",
			Catalog:               "SampleCatalog",
			EnvironmentType:       "Dev",
		}

		store := newEnvironmentStoreForTest(t, mockContext, config, nil)
		env, err := store.Get(*mockContext.Context, "not-found")
		require.ErrorIs(t, err, environment.ErrNotFound)
		require.Nil(t, env)
	})
}

func Test_EnvironmentStore_GetEnvPath(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	config := &Config{
		Name:                  "DEV_CENTER",
		Project:               "Project1",
		EnvironmentDefinition: "WebApp",
		Catalog:               "SampleCatalog",
		EnvironmentType:       "Dev",
	}

	store := newEnvironmentStoreForTest(t, mockContext, config, nil)
	env := environment.New(mockEnvironments[0].Name)
	path := store.EnvPath(env)
	require.Equal(t, fmt.Sprintf("projects/%s/users/me/environments/%s", config.Project, env.GetEnvName()), path)
}

func Test_EnvironmentStore_Save(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	config := &Config{
		Name:                  "DEV_CENTER",
		Project:               "Project1",
		EnvironmentDefinition: "WebApp",
		Catalog:               "SampleCatalog",
		EnvironmentType:       "Dev",
	}

	store := newEnvironmentStoreForTest(t, mockContext, config, nil)
	err := store.Save(*mockContext.Context, environment.New(mockEnvironments[0].Name))
	require.NoError(t, err)
}

func newEnvironmentStoreForTest(
	t *testing.T,
	mockContext *mocks.MockContext,
	config *Config,
	manager Manager,
) environment.RemoteDataStore {
	coreOptions := azsdk.
		DefaultClientOptionsBuilder(*mockContext.Context, mockContext.HttpClient, "azd").
		BuildCoreClientOptions()

	armOptions := azsdk.
		DefaultClientOptionsBuilder(*mockContext.Context, mockContext.HttpClient, "azd").
		BuildArmClientOptions()

	resourceGraphClient, err := armresourcegraph.NewClient(mockContext.Credentials, armOptions)
	require.NoError(t, err)

	devCenterClient, err := devcentersdk.NewDevCenterClient(
		mockContext.Credentials,
		coreOptions,
		resourceGraphClient,
	)

	require.NoError(t, err)

	if manager == nil {
		manager = &mockDevCenterManager{}
	}
	prompter := NewPrompter(config, mockContext.Console, manager, devCenterClient)

	return NewEnvironmentStore(config, devCenterClient, prompter, manager)
}
