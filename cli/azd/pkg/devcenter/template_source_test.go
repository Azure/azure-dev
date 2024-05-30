package devcenter

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockdevcentersdk"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_TemplateSource_ListTemplates(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		templateSource := newTemplateSourceForTest(t, mockContext, &Config{}, nil)
		setupDevCenterSuccessMocks(t, mockContext, templateSource)

		templateList, err := templateSource.ListTemplates(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, templateList)
		require.Len(t, templateList, len(mockEnvDefinitions))
		require.Len(t, templateList[0].Metadata.Project, 4)
		require.Contains(t, templateList[0].Metadata.Project, "platform.type")
		require.Contains(t, templateList[0].Metadata.Project, "platform.config.name")
		require.Contains(t, templateList[0].Metadata.Project, "platform.config.catalog")
		require.Contains(t, templateList[0].Metadata.Project, "platform.config.environmentDefinition")
	})

	t.Run("WithSingleDefaultValue", func(t *testing.T) {
		mockEnvDefinition := &devcentersdk.EnvironmentDefinition{
			Id:           "/projects/Project1/catalogs/SampleCatalog/environmentDefinitions/EnvDefinition_05",
			Name:         "EnvDefinition_05",
			CatalogName:  "SampleCatalog",
			Description:  "Description of EnvDefinition_05",
			TemplatePath: "azuredeploy.json",
			Parameters: []devcentersdk.Parameter{
				{
					Id:      "repoUrl",
					Name:    "repoUrl",
					Type:    devcentersdk.ParameterTypeString,
					Default: "https://github.com/Azure-Samples/todo-nodejs-mongo-swa-func",
				},
			},
		}

		mockContext := mocks.NewMockContext(context.Background())
		templateSource := newTemplateSourceForTest(t, mockContext, &Config{}, nil)
		setupDevCenterSuccessMocks(t, mockContext, templateSource)
		mockdevcentersdk.MockListEnvironmentDefinitions(
			mockContext,
			"Project1",
			[]*devcentersdk.EnvironmentDefinition{mockEnvDefinition},
		)

		templateList, err := templateSource.ListTemplates(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, templateList)
		require.Len(t, templateList, 1)
		require.Len(t, templateList[0].Metadata.Project, 4)
		require.Equal(t, templateList[0].RepositoryPath, mockEnvDefinition.Parameters[0].Default)
		require.Contains(t, templateList[0].Metadata.Project, "platform.type")
		require.Contains(t, templateList[0].Metadata.Project, "platform.config.name")
		require.Contains(t, templateList[0].Metadata.Project, "platform.config.catalog")
		require.Contains(t, templateList[0].Metadata.Project, "platform.config.environmentDefinition")
	})

	t.Run("WithMultipleAllowedValues", func(t *testing.T) {
		mockEnvDefinition := &devcentersdk.EnvironmentDefinition{
			Id:           "/projects/Project1/catalogs/SampleCatalog/environmentDefinitions/EnvDefinition_05",
			Name:         "EnvDefinition_05",
			CatalogName:  "SampleCatalog",
			Description:  "Description of EnvDefinition_05",
			TemplatePath: "azuredeploy.json",
			Parameters: []devcentersdk.Parameter{
				{
					Id:      "repoUrl",
					Name:    "repoUrl",
					Type:    devcentersdk.ParameterTypeString,
					Default: "https://github.com/Azure-Samples/todo-nodejs-mongo-swa-func",
					Allowed: []string{
						"https://github.com/Azure-Samples/todo-nodejs-mongo-swa-func",
						"https://github.com/Azure-Samples/todo-python-mongo-swa-func",
						"https://github.com/Azure-Samples/todo-java-mongo-swa-func",
					},
				},
			},
		}

		mockContext := mocks.NewMockContext(context.Background())
		templateSource := newTemplateSourceForTest(t, mockContext, &Config{}, nil)
		setupDevCenterSuccessMocks(t, mockContext, templateSource)
		mockdevcentersdk.MockListEnvironmentDefinitions(
			mockContext,
			"Project1",
			[]*devcentersdk.EnvironmentDefinition{mockEnvDefinition},
		)

		templateList, err := templateSource.ListTemplates(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, templateList)
		require.Len(t, templateList, len(mockEnvDefinition.Parameters[0].Allowed))
		require.Len(t, templateList[0].Metadata.Project, 4)

		for index := range templateList {
			require.Equal(t, templateList[index].RepositoryPath, mockEnvDefinition.Parameters[0].Allowed[index])
			require.Contains(t, templateList[index].Metadata.Project, "platform.type")
			require.Contains(t, templateList[index].Metadata.Project, "platform.config.name")
			require.Contains(t, templateList[index].Metadata.Project, "platform.config.catalog")
			require.Contains(t, templateList[index].Metadata.Project, "platform.config.environmentDefinition")
		}
	})

	t.Run("NoRepoUrlParameter", func(t *testing.T) {
		mockEnvDefinition := &devcentersdk.EnvironmentDefinition{
			Id:           "/projects/Project1/catalogs/SampleCatalog/environmentDefinitions/EnvDefinition_05",
			Name:         "EnvDefinition_05",
			CatalogName:  "SampleCatalog",
			Description:  "Description of EnvDefinition_05",
			TemplatePath: "azuredeploy.json",
			Parameters:   []devcentersdk.Parameter{},
		}

		mockContext := mocks.NewMockContext(context.Background())
		templateSource := newTemplateSourceForTest(t, mockContext, &Config{}, nil)
		setupDevCenterSuccessMocks(t, mockContext, templateSource)
		mockdevcentersdk.MockListEnvironmentDefinitions(
			mockContext,
			"Project1",
			[]*devcentersdk.EnvironmentDefinition{mockEnvDefinition},
		)

		templateList, err := templateSource.ListTemplates(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, templateList)
		require.Len(t, templateList, 0)
	})

	t.Run("Fail", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		templateSource := newTemplateSourceForTest(t, mockContext, &Config{}, nil)
		setupDevCenterSuccessMocks(t, mockContext, templateSource)
		// Mock will throw 404 not found for this API call causing a failure
		mockdevcentersdk.MockListEnvironmentDefinitions(mockContext, "Project2", nil)

		templateList, err := templateSource.ListTemplates(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, templateList)
	})
}

func Test_TemplateSource_GetTemplate(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		templateSource := newTemplateSourceForTest(t, mockContext, &Config{}, nil)
		setupDevCenterSuccessMocks(t, mockContext, templateSource)

		template, err := templateSource.GetTemplate(*mockContext.Context, "DEV_CENTER_01/SampleCatalog/EnvDefinition_02")
		require.NoError(t, err)
		require.NotNil(t, template)
	})

	t.Run("TemplateNotFound", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		templateSource := newTemplateSourceForTest(t, mockContext, &Config{}, nil)
		setupDevCenterSuccessMocks(t, mockContext, templateSource)

		// Expected to fail because the template path is not found
		template, err := templateSource.GetTemplate(*mockContext.Context, "DEV_CENTER_01/SampleCatalog/NotFound")
		require.ErrorIs(t, err, templates.ErrTemplateNotFound)
		require.Nil(t, template)
	})
}

func setupDevCenterSuccessMocks(t *testing.T, mockContext *mocks.MockContext, templateSource *TemplateSource) {
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, mockDevCenterList)
	mockdevcentersdk.MockListEnvironmentDefinitions(mockContext, "Project1", mockEnvDefinitions)
	mockdevcentersdk.MockListEnvironmentDefinitions(mockContext, "Project2", []*devcentersdk.EnvironmentDefinition{})
	mockdevcentersdk.MockListEnvironmentDefinitions(mockContext, "Project3", []*devcentersdk.EnvironmentDefinition{})
	mockdevcentersdk.MockListEnvironmentDefinitions(mockContext, "Project4", []*devcentersdk.EnvironmentDefinition{})

	if templateSource.manager == nil {
		manager := &mockDevCenterManager{}
		templateSource.manager = manager
	}

	mocks := templateSource.manager.(*mockDevCenterManager)

	mocks.
		On("WritableProjectsWithFilter", *mockContext.Context, mock.Anything, mock.Anything).
		Return(mockProjects, nil)
}

func newTemplateSourceForTest(
	t *testing.T,
	mockContext *mocks.MockContext,
	config *Config,
	manager Manager,
) *TemplateSource {
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

	return NewTemplateSource(config, manager, devCenterClient).(*TemplateSource)
}
