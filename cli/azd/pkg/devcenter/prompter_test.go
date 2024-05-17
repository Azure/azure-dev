package devcenter

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockdevcentersdk"
	"github.com/stretchr/testify/require"
)

func Test_Prompt_Project(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		selectedDevCenter := mockDevCenterList[0]
		selectedProjectIndex := 1

		manager := &mockDevCenterManager{}
		manager.
			On("WritableProjects", *mockContext.Context).
			Return(mockProjects, nil)

		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Select a project")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return selectedProjectIndex, nil
		})

		prompter := newPrompterForTest(t, mockContext, manager)
		selectedProject, err := prompter.PromptProject(*mockContext.Context, selectedDevCenter.Name)
		require.NoError(t, err)
		require.NotNil(t, selectedProject)
		require.Equal(t, mockProjects[selectedProjectIndex], selectedProject)
	})

	t.Run("NoProjects", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		manager := &mockDevCenterManager{}
		manager.
			On("WritableProjects", *mockContext.Context).
			Return([]*devcentersdk.Project{}, nil)

		prompter := newPrompterForTest(t, mockContext, manager)
		selectedProject, err := prompter.PromptProject(*mockContext.Context, "")
		require.Error(t, err)
		require.ErrorContains(t, err, "no dev center projects found")
		require.Nil(t, selectedProject)
	})
}

func Test_Prompt_EnvironmentType(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		selectedDevCenter := mockDevCenterList[0]
		selectedProject := mockProjects[1]

		selectedIndex := 3

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockListEnvironmentTypes(mockContext, selectedProject.Name, mockEnvironmentTypes)

		manager := &mockDevCenterManager{}
		manager.
			On("WritableProjects", *mockContext.Context).
			Return(mockProjects, nil)

		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Select an environment type")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return selectedIndex, nil
		})

		prompter := newPrompterForTest(t, mockContext, manager)
		selectedEnvironmentType, err := prompter.PromptEnvironmentType(
			*mockContext.Context,
			selectedDevCenter.Name,
			selectedProject.Name,
		)
		require.NoError(t, err)
		require.NotNil(t, selectedEnvironmentType)
		require.Equal(t, mockEnvironmentTypes[selectedIndex], selectedEnvironmentType)
	})

	t.Run("NoEnvironmentTypes", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		selectedDevCenter := mockDevCenterList[0]
		selectedProject := mockProjects[1]

		selectedIndex := 3

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockListEnvironmentTypes(mockContext, selectedProject.Name, []*devcentersdk.EnvironmentType{})

		manager := &mockDevCenterManager{}
		manager.
			On("WritableProjects", *mockContext.Context).
			Return(mockProjects, nil)

		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Select an environment type")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return selectedIndex, nil
		})

		prompter := newPrompterForTest(t, mockContext, manager)
		selectedEnvironmentType, err := prompter.PromptEnvironmentType(
			*mockContext.Context,
			selectedDevCenter.Name,
			selectedProject.Name,
		)
		require.Error(t, err)
		require.ErrorContains(t, err, "no environment types found")
		require.Nil(t, selectedEnvironmentType)
	})
}

func Test_Prompt_EnvironmentDefinitions(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		selectedDevCenter := mockDevCenterList[0]
		selectedProject := mockProjects[1]

		selectedIndex := 2

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockListEnvironmentDefinitions(mockContext, selectedProject.Name, mockEnvDefinitions)

		manager := &mockDevCenterManager{}
		manager.
			On("WritableProjects", *mockContext.Context).
			Return(mockProjects, nil)

		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Select an environment definition")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return selectedIndex, nil
		})

		prompter := newPrompterForTest(t, mockContext, manager)
		selectedEnvironmentType, err := prompter.PromptEnvironmentDefinition(
			*mockContext.Context,
			selectedDevCenter.Name,
			selectedProject.Name,
		)
		require.NoError(t, err)
		require.NotNil(t, selectedEnvironmentType)
		require.Equal(t, mockEnvDefinitions[selectedIndex], selectedEnvironmentType)
	})

	t.Run("NoEnvironmentDefinitions", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		selectedDevCenter := mockDevCenterList[0]
		selectedProject := mockProjects[1]

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockListEnvironmentDefinitions(
			mockContext,
			selectedProject.Name,
			[]*devcentersdk.EnvironmentDefinition{},
		)

		manager := &mockDevCenterManager{}
		manager.
			On("WritableProjects", *mockContext.Context).
			Return(mockProjects, nil)

		prompter := newPrompterForTest(t, mockContext, manager)
		selectedEnvironmentType, err := prompter.PromptEnvironmentDefinition(
			*mockContext.Context,
			selectedDevCenter.Name,
			selectedProject.Name,
		)
		require.Error(t, err)
		require.ErrorContains(t, err, "no environment definitions found")
		require.Nil(t, selectedEnvironmentType)
	})
}

func Test_Prompt_Config(t *testing.T) {
	t.Run("AllValuesSet", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		selectedDevCenter := mockDevCenterList[0]
		selectedProject := mockProjects[1]
		selectedEnvDefinition := mockEnvDefinitions[2]

		config := &Config{
			Name:                  selectedDevCenter.Name,
			Project:               selectedProject.Name,
			EnvironmentDefinition: selectedEnvDefinition.Name,
			Catalog:               selectedEnvDefinition.CatalogName,
		}

		prompter := newPrompterForTest(t, mockContext, nil)
		err := prompter.PromptForConfig(*mockContext.Context, config)
		require.NoError(t, err)
		require.NotNil(t, config)
		require.Equal(t, selectedDevCenter.Name, config.Name)
		require.Equal(t, selectedProject.Name, config.Project)
		require.Equal(t, selectedEnvDefinition.Name, config.EnvironmentDefinition)
		require.Equal(t, selectedEnvDefinition.CatalogName, config.Catalog)
	})

	t.Run("NoValuesSet", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		selectedDevCenter := mockDevCenterList[0]
		selectedProject := mockProjects[1]
		selectedEnvDefinition := mockEnvDefinitions[2]

		mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
		mockdevcentersdk.MockListEnvironmentDefinitions(mockContext, selectedProject.Name, mockEnvDefinitions)

		manager := &mockDevCenterManager{}
		manager.
			On("WritableProjects", *mockContext.Context).
			Return(mockProjects, nil)

		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "project")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 1, nil
		})

		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "environment definition")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 2, nil
		})

		config := &Config{}

		prompter := newPrompterForTest(t, mockContext, manager)
		err := prompter.PromptForConfig(*mockContext.Context, config)
		require.NoError(t, err)
		require.NotNil(t, config)
		require.Equal(t, selectedDevCenter.Name, config.Name)
		require.Equal(t, selectedProject.Name, config.Project)
		require.Equal(t, selectedEnvDefinition.Name, config.EnvironmentDefinition)
		require.Equal(t, selectedEnvDefinition.CatalogName, config.Catalog)
	})
}

func Test_Prompt_Parameters(t *testing.T) {
	type paramWithValue struct {
		devcentersdk.Parameter
		userValue     any
		expectedValue any
	}

	t.Run("MultipleParameters", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		promptedParams := map[string]bool{}

		expectedValues := map[string]paramWithValue{
			"param1": {
				Parameter:     devcentersdk.Parameter{Id: "param1", Name: "Param 1", Type: devcentersdk.ParameterTypeString},
				userValue:     "value1",
				expectedValue: "value1",
			},
			"param2": {
				Parameter:     devcentersdk.Parameter{Id: "param2", Name: "Param 2", Type: devcentersdk.ParameterTypeString},
				userValue:     "value2",
				expectedValue: "value2",
			},
			"param3": {
				Parameter:     devcentersdk.Parameter{Id: "param3", Name: "Param 3", Type: devcentersdk.ParameterTypeBool},
				userValue:     true,
				expectedValue: true,
			},
			"param4": {
				Parameter:     devcentersdk.Parameter{Id: "param4", Name: "Param 4", Type: devcentersdk.ParameterTypeInt},
				userValue:     "123",
				expectedValue: 123,
			},
		}

		var mockPrompt = func(key string, param paramWithValue) {
			if param.Type == devcentersdk.ParameterTypeBool {
				mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
					return strings.Contains(options.Message, param.Name)
				}).RespondFn(func(options input.ConsoleOptions) (any, error) {
					promptedParams[key] = true
					return param.userValue, nil
				})
			} else {
				mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
					return strings.Contains(options.Message, param.Name)
				}).RespondFn(func(options input.ConsoleOptions) (any, error) {
					promptedParams[key] = true
					return param.userValue, nil
				})
			}
		}

		for key, param := range expectedValues {
			mockPrompt(key, param)
		}

		env := environment.New("Test")
		envDefinition := &devcentersdk.EnvironmentDefinition{
			Parameters: []devcentersdk.Parameter{
				{
					Id:   "param1",
					Name: "Param 1",
					Type: devcentersdk.ParameterTypeString,
				},
				{
					Id:   "param2",
					Name: "Param 2",
					Type: devcentersdk.ParameterTypeString,
				},
				{
					Id:   "param3",
					Name: "Param 3",
					Type: devcentersdk.ParameterTypeBool,
				},
				{
					Id:   "param4",
					Name: "Param 4",
					Type: devcentersdk.ParameterTypeInt,
				},
			},
		}

		prompter := newPrompterForTest(t, mockContext, nil)
		values, err := prompter.PromptParameters(*mockContext.Context, env, envDefinition)
		require.NoError(t, err)

		for key, value := range values {
			require.Equal(t, expectedValues[key].expectedValue, value)
		}
	})

	t.Run("WithSomeSetValues", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prompter := newPrompterForTest(t, mockContext, nil)
		promptCalled := false

		// Only mock response for param 3
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Param 3")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			promptCalled = true
			return "value3", nil
		})

		env := environment.New("Test")
		envDefinition := &devcentersdk.EnvironmentDefinition{
			Parameters: []devcentersdk.Parameter{
				{
					Id:   "param1",
					Name: "Param 1",
					Type: devcentersdk.ParameterTypeString,
				},
				{
					Id:   "param2",
					Name: "Param 2",
					Type: devcentersdk.ParameterTypeString,
				},
				{
					Id:   "param3",
					Name: "Param 3",
					Type: devcentersdk.ParameterTypeString,
				},
			},
		}

		_ = env.Config.Set("provision.parameters.param1", "value1")
		_ = env.Config.Set("provision.parameters.param2", "value2")

		values, err := prompter.PromptParameters(*mockContext.Context, env, envDefinition)
		require.NoError(t, err)
		require.True(t, promptCalled)
		require.Equal(t, "value1", values["param1"])
		require.Equal(t, "value2", values["param2"])
		require.Equal(t, "value3", values["param3"])
	})

	t.Run("WithAllSetValues", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prompter := newPrompterForTest(t, mockContext, nil)

		env := environment.New("Test")
		envDefinition := &devcentersdk.EnvironmentDefinition{
			Parameters: []devcentersdk.Parameter{
				{
					Id:   "param1",
					Name: "Param 1",
					Type: devcentersdk.ParameterTypeString,
				},
				{
					Id:   "param2",
					Name: "Param 2",
					Type: devcentersdk.ParameterTypeString,
				},
			},
		}

		_ = env.Config.Set("provision.parameters.param1", "value1")
		_ = env.Config.Set("provision.parameters.param2", "value2")

		values, err := prompter.PromptParameters(*mockContext.Context, env, envDefinition)
		require.NoError(t, err)
		require.Equal(t, "value1", values["param1"])
		require.Equal(t, "value2", values["param2"])
	})
}

func newPrompterForTest(t *testing.T, mockContext *mocks.MockContext, manager Manager) *Prompter {
	resourceGraphClient, err := armresourcegraph.NewClient(mockContext.Credentials, mockContext.ArmClientOptions)
	require.NoError(t, err)

	devCenterClient, err := devcentersdk.NewDevCenterClient(
		mockContext.Credentials,
		mockContext.CoreClientOptions,
		resourceGraphClient,
		cloud.AzurePublic(),
	)

	require.NoError(t, err)

	return NewPrompter(mockContext.Console, manager, devCenterClient)
}
