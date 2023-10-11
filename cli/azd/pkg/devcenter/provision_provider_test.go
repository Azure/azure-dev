package devcenter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockdevcentersdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/require"
)

func Test_ProvisionProvider_Initialize(t *testing.T) {
	t.Run("AllValuesSet", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		config := &Config{
			Name:                  "DEV_CENTER_01",
			Catalog:               "SampleCatalog",
			Project:               "Project1",
			EnvironmentType:       "Dev",
			EnvironmentDefinition: "WebApp",
			User:                  "me",
		}
		env := environment.New("test")
		configMap, err := MarshalConfig(config)
		require.NoError(t, err)
		_ = env.Config.Set("platform.config", configMap)

		provider := newProvisionProviderForTest(t, mockContext, config, env)
		err = provider.Initialize(*mockContext.Context, "project/path", provisioning.Options{})
		require.NoError(t, err)
	})

	t.Run("PromptMissingEnvironmentType", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		config := &Config{
			Name:                  "DEV_CENTER_01",
			Catalog:               "SampleCatalog",
			Project:               "Project1",
			EnvironmentDefinition: "WebApp",
			User:                  "me",
		}
		env := environment.New("test")
		configMap, err := MarshalConfig(config)
		require.NoError(t, err)
		_ = env.Config.Set("platform.config", configMap)

		selectedEnvironmentTypeIndex := 1
		selectedEnvironmentType := mockEnvironmentTypes[selectedEnvironmentTypeIndex]

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockListEnvironmentTypes(mockContext, config.Project, mockEnvironmentTypes)
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "environment type")
		}).Respond(selectedEnvironmentTypeIndex)

		provider := newProvisionProviderForTest(t, mockContext, config, env)
		err = provider.Initialize(*mockContext.Context, "project/path", provisioning.Options{})
		require.NoError(t, err)

		actualEnvironmentType, ok := env.Config.Get(DevCenterEnvTypePath)
		require.True(t, ok)
		require.Equal(t, selectedEnvironmentType.Name, actualEnvironmentType)
	})
}

func Test_ProvisionProvider_Deploy(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	config := &Config{
		Name:                  "DEV_CENTER_01",
		Catalog:               "SampleCatalog",
		Project:               "Project1",
		EnvironmentType:       "Dev",
		EnvironmentDefinition: "WebApp",
		User:                  "me",
	}
	env := environment.New("test")

	provider := newProvisionProviderForTest(t, mockContext, config, env)
	err := provider.Initialize(*mockContext.Context, "project/path", provisioning.Options{})
	require.NoError(t, err)

	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
	mockdevcentersdk.MockGetEnvironmentDefinition(mockContext, config.Project, config.Catalog, config.EnvironmentDefinition, mockEnvDefinitions[0])
	mockdevcentersdk.MockGetEnvironment(mockContext, config.Project, config.User, env.GetEnvName(), mockEnvironments[0])
	mockdevcentersdk.MockPutEnvironment(mockContext, config.Project, config.User, env.GetEnvName(), &devcentersdk.OperationStatus{
		Id:        "id",
		Name:      mockEnvironments[0].Name,
		Status:    "Succeeded",
		StartTime: time.Now(),
		EndTime:   time.Now(),
	})

	result, err := provider.Deploy(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func newProvisionProviderForTest(t *testing.T, mockContext *mocks.MockContext, config *Config, env *environment.Environment) provisioning.Provider {
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

	azCli := azcli.NewAzCli(mockContext.SubscriptionCredentialProvider, mockContext.HttpClient, azcli.NewAzCliArgs{})
	resourceManager := infra.NewAzureResourceManager(azCli, azapi.NewDeploymentOperations(mockContext.SubscriptionCredentialProvider, mockContext.HttpClient))
	devCenterManager := &mockDevCenterManager{}

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", *mockContext.Context, env).Return(nil)

	prompter := NewPrompter(config, mockContext.Console, devCenterManager, devCenterClient)

	return NewProvisionProvider(mockContext.Console, env, envManager, config, devCenterClient, resourceManager, devCenterManager, prompter)
}
