package devcenter

import (
	"context"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockdevcentersdk"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var mockEnvironments []*devcentersdk.Environment = []*devcentersdk.Environment{
	{
		ProvisioningState:         "Succeeded",
		ResourceGroupId:           "/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_NAME",
		Name:                      "user01-project1-dev-01",
		CatalogName:               "SampleCatalog",
		EnvironmentDefinitionName: "WebApp",
		EnvironmentType:           "Dev",
		User:                      "me",
		Parameters: map[string]any{
			"stringParam": "value",
			"boolParam":   true,
			"numberParam": 42,
		},
	},
	{
		ProvisioningState:         "Succeeded",
		ResourceGroupId:           "/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_NAME",
		Name:                      "user01-project1-dev-02",
		CatalogName:               "SampleCatalog",
		EnvironmentDefinitionName: "WebApp",
		EnvironmentType:           "Dev",
		User:                      "me",
		Parameters: map[string]any{
			"stringParam": "value",
			"boolParam":   true,
			"numberParam": 42,
		},
	},
	{
		ProvisioningState:         "Succeeded",
		ResourceGroupId:           "/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_NAME",
		Name:                      "user01-project1-dev-03",
		CatalogName:               "SampleCatalog",
		EnvironmentDefinitionName: "ContainerApp",
		EnvironmentType:           "Dev",
		User:                      "me",
		Parameters: map[string]any{
			"stringParam": "value",
			"boolParam":   true,
			"numberParam": 42,
		},
	},
}

func Test_EnvironmentStore_List(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
	mockdevcentersdk.MockListEnvironmentsByProject(mockContext, "Project1", mockEnvironments)

	t.Run("AllMatchingEnvironments", func(t *testing.T) {
		config := &Config{
			Name:                  "DEV_CENTER_01",
			Project:               "Project1",
			EnvironmentDefinition: "WebApp",
			Catalog:               "SampleCatalog",
			EnvironmentType:       "Dev",
			User:                  "me",
		}

		store := newEnvironmentStoreForTest(t, mockContext, config, nil)
		envList, err := store.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
		require.Len(t, envList, 2)
	})

	t.Run("SomeMatchingEnvironments", func(t *testing.T) {
		config := &Config{
			Name:                  "DEV_CENTER_01",
			Project:               "Project1",
			EnvironmentDefinition: "ContainerApp",
			Catalog:               "SampleCatalog",
			EnvironmentType:       "Dev",
			User:                  "me",
		}

		store := newEnvironmentStoreForTest(t, mockContext, config, nil)
		envList, err := store.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
		require.Len(t, envList, 1)
	})

	t.Run("NoMatchingEnvironments", func(t *testing.T) {
		config := &Config{
			Name:                  "DEV_CENTER_01",
			Project:               "Project1",
			EnvironmentDefinition: "FunctionApp",
			Catalog:               "SampleCatalog",
			EnvironmentType:       "Dev",
			User:                  "me",
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
	mockdevcentersdk.MockListEnvironmentsByProject(mockContext, "Project1", mockEnvironments)

	t.Run("Exists", func(t *testing.T) {
		mockdevcentersdk.MockGetEnvironment(mockContext, "Project1", "me", mockEnvironments[0].Name, mockEnvironments[0])

		// Config EnvironmentType & User intentionally omitted and should be set in environment after sync
		config := &Config{
			Name:                  "DEV_CENTER_01",
			Project:               "Project1",
			EnvironmentDefinition: "WebApp",
			Catalog:               "SampleCatalog",
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
			On("Outputs",
				*mockContext.Context,
				mock.AnythingOfType("*devcenter.Config"),
				mock.AnythingOfType("*devcentersdk.Environment")).
			Return(outputs, nil)

		store := newEnvironmentStoreForTest(t, mockContext, config, manager)
		env, err := store.Get(*mockContext.Context, mockEnvironments[0].Name)
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, mockEnvironments[0].Name, env.Name())
		require.Equal(t, "value1", env.Getenv("KEY1"))
		require.Equal(t, "value2", env.Getenv("KEY2"))

		devCenterNode, ok := env.Config.Get("platform.config")
		require.True(t, ok)

		for key, expected := range mockEnvironments[0].Parameters {
			paramPath := fmt.Sprintf("%s.%s", ProvisionParametersConfigPath, key)
			actual, ok := env.Config.Get(paramPath)
			require.True(t, ok)
			require.Equal(t, fmt.Sprint(expected), fmt.Sprint(actual))
		}

		envConfig, err := ParseConfig(devCenterNode)
		require.NoError(t, err)
		require.Equal(t, envConfig.EnvironmentType, mockEnvironments[0].EnvironmentType)
		require.Equal(t, envConfig.User, mockEnvironments[0].User)

	})

	t.Run("DoesNotExist", func(t *testing.T) {
		config := &Config{
			Name:                  "DEV_CENTER_01",
			Project:               "Project1",
			EnvironmentDefinition: "WebApp",
			Catalog:               "SampleCatalog",
			EnvironmentType:       "Dev",
			User:                  "me",
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
		Name:                  "DEV_CENTER_01",
		Project:               "Project1",
		EnvironmentDefinition: "WebApp",
		Catalog:               "SampleCatalog",
		EnvironmentType:       "Dev",
		User:                  "me",
	}

	store := newEnvironmentStoreForTest(t, mockContext, config, nil)
	env := environment.New(mockEnvironments[0].Name)
	path := store.EnvPath(env)
	require.Equal(t, fmt.Sprintf("projects/%s/users/me/environments/%s", config.Project, env.Name()), path)
}

func Test_EnvironmentStore_Save(t *testing.T) {
	tests := []struct {
		name     string
		env      *environment.Environment
		config   *Config
		isNew    bool
		validate func(t *testing.T, env *environment.Environment)
	}{
		{
			name: "NewEnvironment",
			env: environment.NewWithValues(mockEnvironments[0].Name, map[string]string{
				environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
				environment.ResourceGroupEnvVarName:  "RESOURCE_GROUP_NAME",
			}),
			isNew: true,
			config: &Config{
				Name:                  "DEV_CENTER_01",
				Project:               "Project1",
				EnvironmentDefinition: "WebApp",
				Catalog:               "SampleCatalog",
				EnvironmentType:       "Dev",
				User:                  "me",
			},
			validate: func(t *testing.T, env *environment.Environment) {
				// Before provisioning we do know know the subscription id or resource group name
				// At this point we should not persist any addidtional project information in the azd config.
				_, hasProject := env.Config.Get(DevCenterProjectPath)
				_, hasEnvType := env.Config.Get(DevCenterEnvTypePath)

				require.False(t, hasProject)
				require.False(t, hasEnvType)
			},
		},
		{
			name: "ExistingEnvironment",
			env: environment.NewWithValues(mockEnvironments[0].Name, map[string]string{
				environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
				environment.ResourceGroupEnvVarName:  "RESOURCE_GROUP_NAME",
			}),
			isNew: false,
			config: &Config{
				Name:                  "DEV_CENTER_01",
				Project:               "Project1",
				EnvironmentDefinition: "WebApp",
				Catalog:               "SampleCatalog",
				EnvironmentType:       "Dev",
				User:                  "me",
			},
			validate: func(t *testing.T, env *environment.Environment) {
				// After provisioning completes the subscription id and resource group name are stored in the azd environment
				// At this point azd should also persist the devcenter project and environment type in the config
				project, projectOk := env.Config.Get(DevCenterProjectPath)
				envType, envTypeOk := env.Config.Get(DevCenterEnvTypePath)

				require.True(t, projectOk)
				require.Equal(t, "Project1", project)
				require.True(t, envTypeOk)
				require.Equal(t, "Dev", envType)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			store := newEnvironmentStoreForTest(t, mockContext, test.config, nil)
			err := store.Save(*mockContext.Context, test.env, &environment.SaveOptions{IsNew: test.isNew})
			require.NoError(t, err)
			test.validate(t, test.env)
		})
	}
}

func newEnvironmentStoreForTest(
	t *testing.T,
	mockContext *mocks.MockContext,
	devCenterConfig *Config,
	manager Manager,
) environment.RemoteDataStore {
	resourceGraphClient, err := armresourcegraph.NewClient(mockContext.Credentials, mockContext.ArmClientOptions)
	require.NoError(t, err)

	devCenterClient, err := devcentersdk.NewDevCenterClient(
		mockContext.Credentials,
		mockContext.CoreClientOptions,
		resourceGraphClient,
		cloud.AzurePublic(),
	)

	require.NoError(t, err)

	if manager == nil {
		manager = &mockDevCenterManager{}
	}
	prompter := NewPrompter(mockContext.Console, manager, devCenterClient)

	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	dataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)

	return NewEnvironmentStore(devCenterConfig, devCenterClient, prompter, manager, dataStore)
}
