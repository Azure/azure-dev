package devcenter

import (
	"context"
	"errors"
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
	"github.com/stretchr/testify/mock"
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

		provider := newProvisionProviderForTest(t, mockContext, config, env, nil)
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

		provider := newProvisionProviderForTest(t, mockContext, config, env, nil)
		err = provider.Initialize(*mockContext.Context, "project/path", provisioning.Options{})
		require.NoError(t, err)

		actualEnvironmentType, ok := env.Config.Get(DevCenterEnvTypePath)
		require.True(t, ok)
		require.Equal(t, selectedEnvironmentType.Name, actualEnvironmentType)
	})
}

func Test_ProvisionProvider_Deploy(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
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

		outputParams := map[string]provisioning.OutputParameter{
			"PARAM_01": {Type: provisioning.ParameterTypeString, Value: "value1"},
			"PARAM_02": {Type: provisioning.ParameterTypeString, Value: "value2"},
			"PARAM_03": {Type: provisioning.ParameterTypeString, Value: "value3"},
			"PARAM_04": {Type: provisioning.ParameterTypeString, Value: "value4"},
		}

		manager := &mockDevCenterManager{}
		manager.
			On("Outputs", *mockContext.Context, mock.AnythingOfType("*devcentersdk.Environment")).
			Return(outputParams, nil)

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

		provider := newProvisionProviderForTest(t, mockContext, config, env, manager)
		result, err := provider.Deploy(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, result.Deployment.Outputs, outputParams)
		require.Len(t, result.Deployment.Parameters, len(mockEnvDefinitions[0].Parameters))
	})

	t.Run("SuccessWithPrompts", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		selectedEnvironmentTypeIndex := 2

		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "environment type")
		}).Respond(selectedEnvironmentTypeIndex)

		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Param")
		}).Respond("value")

		// Missing environment type, prompt user
		config := &Config{
			Name:                  "DEV_CENTER_01",
			Catalog:               "SampleCatalog",
			Project:               "Project1",
			EnvironmentDefinition: "WebApp",
			User:                  "me",
		}
		env := environment.New("test")

		outputParams := map[string]provisioning.OutputParameter{
			"PARAM_01": {Type: provisioning.ParameterTypeString, Value: "value1"},
			"PARAM_02": {Type: provisioning.ParameterTypeString, Value: "value2"},
			"PARAM_03": {Type: provisioning.ParameterTypeString, Value: "value3"},
			"PARAM_04": {Type: provisioning.ParameterTypeString, Value: "value4"},
		}

		manager := &mockDevCenterManager{}
		manager.
			On("Outputs", *mockContext.Context, mock.AnythingOfType("*devcentersdk.Environment")).
			Return(outputParams, nil)

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockListEnvironmentTypes(mockContext, config.Project, mockEnvironmentTypes)
		mockdevcentersdk.MockGetEnvironmentDefinition(mockContext, config.Project, config.Catalog, config.EnvironmentDefinition, mockEnvDefinitions[3])
		mockdevcentersdk.MockGetEnvironment(mockContext, config.Project, config.User, env.GetEnvName(), mockEnvironments[0])
		mockdevcentersdk.MockPutEnvironment(mockContext, config.Project, config.User, env.GetEnvName(), &devcentersdk.OperationStatus{
			Id:        "id",
			Name:      mockEnvironments[0].Name,
			Status:    "Succeeded",
			StartTime: time.Now(),
			EndTime:   time.Now(),
		})

		provider := newProvisionProviderForTest(t, mockContext, config, env, manager)

		err := provider.Initialize(*mockContext.Context, "project/path", provisioning.Options{})
		require.NoError(t, err)

		result, err := provider.Deploy(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, mockEnvironmentTypes[selectedEnvironmentTypeIndex].Name, config.EnvironmentType)
		require.Equal(t, result.Deployment.Outputs, outputParams)
		require.Len(t, result.Deployment.Parameters, len(mockEnvDefinitions[3].Parameters))
		require.Equal(t, "value", result.Deployment.Parameters["param01"].Value)
		require.Equal(t, "value", result.Deployment.Parameters["param02"].Value)
	})

	t.Run("FailedCreatingEnvironment", func(t *testing.T) {
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

		provider := newProvisionProviderForTest(t, mockContext, config, env, nil)

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockGetEnvironmentDefinition(mockContext, config.Project, config.Catalog, config.EnvironmentDefinition, mockEnvDefinitions[0])
		mockdevcentersdk.MockGetEnvironment(mockContext, config.Project, config.User, env.GetEnvName(), nil)
		mockdevcentersdk.MockPutEnvironment(mockContext, config.Project, config.User, env.GetEnvName(), &devcentersdk.OperationStatus{
			Id:        "id",
			Name:      mockEnvironments[0].Name,
			Status:    "Failed",
			StartTime: time.Now(),
			EndTime:   time.Now(),
		})

		result, err := provider.Deploy(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, result)
	})
}

func Test_ProvisionProvider_State(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
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

		outputParams := map[string]provisioning.OutputParameter{
			"PARAM_01": {Type: provisioning.ParameterTypeString, Value: "value1"},
			"PARAM_02": {Type: provisioning.ParameterTypeString, Value: "value2"},
			"PARAM_03": {Type: provisioning.ParameterTypeString, Value: "value3"},
			"PARAM_04": {Type: provisioning.ParameterTypeString, Value: "value4"},
		}

		manager := &mockDevCenterManager{}
		manager.
			On("Outputs", *mockContext.Context, mock.AnythingOfType("*devcentersdk.Environment")).
			Return(outputParams, nil)

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockGetEnvironment(mockContext, config.Project, config.User, env.GetEnvName(), mockEnvironments[0])

		provider := newProvisionProviderForTest(t, mockContext, config, env, manager)
		result, err := provider.State(*mockContext.Context, &provisioning.StateOptions{})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.State.Outputs, len(outputParams))
	})

	t.Run("EnvironmentNotFound", func(t *testing.T) {
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

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockGetEnvironment(mockContext, config.Project, config.User, env.GetEnvName(), nil)

		provider := newProvisionProviderForTest(t, mockContext, config, env, nil)
		result, err := provider.State(*mockContext.Context, &provisioning.StateOptions{})
		require.Error(t, err)
		require.Nil(t, result)
	})

	t.Run("NoDeploymentOutputs", func(t *testing.T) {
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

		manager := &mockDevCenterManager{}
		manager.
			On("Outputs", *mockContext.Context, mock.AnythingOfType("*devcentersdk.Environment")).
			Return(nil, errors.New("no outputs"))

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockGetEnvironment(mockContext, config.Project, config.User, env.GetEnvName(), mockEnvironments[0])

		provider := newProvisionProviderForTest(t, mockContext, config, env, manager)
		result, err := provider.State(*mockContext.Context, &provisioning.StateOptions{})
		require.Error(t, err)
		require.Nil(t, result)
	})
}

func Test_ProvisionProvider_Destroy(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
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

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockDeleteEnvironment(mockContext, config.Project, config.User, env.GetEnvName(), &devcentersdk.OperationStatus{
			Id:        "id",
			Name:      mockEnvironments[0].Name,
			Status:    "Succeeded",
			StartTime: time.Now(),
			EndTime:   time.Now(),
		})

		provider := newProvisionProviderForTest(t, mockContext, config, env, nil)
		destroyOptions := provisioning.NewDestroyOptions(true, true)
		result, err := provider.Destroy(*mockContext.Context, destroyOptions)
		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("DeploymentNotFound", func(t *testing.T) {
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

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockDeleteEnvironment(mockContext, config.Project, config.User, env.GetEnvName(), nil)

		provider := newProvisionProviderForTest(t, mockContext, config, env, nil)
		destroyOptions := provisioning.NewDestroyOptions(true, true)
		result, err := provider.Destroy(*mockContext.Context, destroyOptions)
		require.Error(t, err)
		require.Nil(t, result)
	})
}

func Test_ProvisionProvider_Preview(t *testing.T) {
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

	provider := newProvisionProviderForTest(t, mockContext, config, env, nil)

	// Preview is not supported in ADE - expected to fail
	result, err := provider.Preview(*mockContext.Context)
	require.Error(t, err)
	require.Nil(t, result)
}

func newProvisionProviderForTest(t *testing.T, mockContext *mocks.MockContext, config *Config, env *environment.Environment, manager Manager) provisioning.Provider {
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

	if manager == nil {
		manager = &mockDevCenterManager{}
	}

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", *mockContext.Context, env).Return(nil)

	prompter := NewPrompter(config, mockContext.Console, manager, devCenterClient)

	return NewProvisionProvider(mockContext.Console, env, envManager, config, devCenterClient, resourceManager, manager, prompter)
}
