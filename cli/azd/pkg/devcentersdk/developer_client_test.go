package devcentersdk

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_DevCenter_Client(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	authManager, err := auth.NewManager(
		config.NewManager(),
		config.NewUserConfigManager(),
		http.DefaultClient,
		mockContext.Console,
	)

	credentials, err := authManager.CredentialForCurrentUser(*mockContext.Context, nil)
	require.NoError(t, err)

	options := azsdk.
		DefaultClientOptionsBuilder(*mockContext.Context, http.DefaultClient, "azd").
		BuildCoreClientOptions()

	client, err := NewDevCenterClient(credentials, options)
	require.NoError(t, err)

	// Get dev center list
	devCenterList, err := client.
		DevCenters().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, devCenterList)

	index := slices.IndexFunc(devCenterList.Value, func(devCenter *DevCenter) bool {
		return devCenter.Name == "wabrez-devcenter"
	})
	matchingDevCenter := devCenterList.Value[index]

	// Get project list
	projectList, err := client.
		DevCenterByEndpoint(matchingDevCenter.ServiceUri).
		Projects().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, projectList)

	// Get project by name
	project, err := client.
		DevCenterByEndpoint(devCenterList.Value[0].Id).
		ProjectByName(projectList.Value[0].Name).
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotNil(t, project)

	// Get Catalog List
	catalogList, err := client.
		DevCenterByEndpoint(devCenterList.Value[0].Id).
		ProjectByName(projectList.Value[0].Name).
		Catalogs().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, catalogList)

	// Get Catalog by name
	catalog, err := client.
		DevCenterByEndpoint(devCenterList.Value[0].Id).
		ProjectByName(projectList.Value[0].Name).
		CatalogByName(catalogList.Value[0].Name).
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotNil(t, catalog)

	// Get Environment Type List
	environmentTypeList, err := client.
		DevCenterByEndpoint(devCenterList.Value[0].Id).
		ProjectByName(projectList.Value[0].Name).
		EnvironmentTypes().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, environmentTypeList)

	// Get Environment Definition List
	environmentDefinitionList, err := client.
		DevCenterByEndpoint(devCenterList.Value[0].Id).
		ProjectByName(projectList.Value[0].Name).
		EnvironmentDefinitions().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, environmentDefinitionList)
}
