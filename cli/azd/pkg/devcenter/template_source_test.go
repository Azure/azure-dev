package devcenter

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
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
		require.Contains(t, templateList[0].Metadata.Project, "devCenter.name")
		require.Contains(t, templateList[0].Metadata.Project, "devCenter.catalog")
		require.Contains(t, templateList[0].Metadata.Project, "devCenter.environmentDefinition")
		require.Contains(t, templateList[0].Metadata.Project, "devCenter.repoUrl")
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

		template, err := templateSource.GetTemplate(*mockContext.Context, "DEV_CENTER_01/SampleCatalog/WebApp")
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

	return NewTemplateSource(config, manager, devCenterClient).(*TemplateSource)
}
