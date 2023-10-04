package devcentersdk

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_DevCenter_Client(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("Skipping DevCenter tests in CI")
	}

	mockContext := mocks.NewMockContext(context.Background())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	authManager, err := auth.NewManager(
		fileConfigManager,
		config.NewUserConfigManager(fileConfigManager),
		http.DefaultClient,
		mockContext.Console,
	)
	require.NoError(t, err)

	credentials, err := authManager.CredentialForCurrentUser(*mockContext.Context, nil)
	require.NoError(t, err)

	options := azsdk.
		DefaultClientOptionsBuilder(*mockContext.Context, http.DefaultClient, "azd").
		BuildCoreClientOptions()

	armOptions := azsdk.
		DefaultClientOptionsBuilder(*mockContext.Context, http.DefaultClient, "azd").
		BuildArmClientOptions()

	resourceGraphClient, err := armresourcegraph.NewClient(credentials, armOptions)
	require.NoError(t, err)

	client, err := NewDevCenterClient(credentials, options, resourceGraphClient)
	require.NoError(t, err)

	// Get dev center list
	devCenterList, err := client.
		DevCenters().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, devCenterList)

	devCenterName := "wabrez-devcenter"
	devCenterClient := client.DevCenterByName(devCenterName)

	// Get project list
	projectList, err := devCenterClient.
		Projects().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, projectList)

	projectName := "Project1"
	projectClient := devCenterClient.ProjectByName(projectName)

	// Get project by name
	project, err := projectClient.
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotNil(t, project)

	permissions, err := projectClient.
		Permissions().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, permissions)

	// Get Catalog List
	catalogList, err := projectClient.
		Catalogs().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, catalogList)

	// Get Catalog by name
	catalog, err := projectClient.
		CatalogByName("SampleCatalog").
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotNil(t, catalog)

	// Get Environment Type List
	environmentTypeList, err := projectClient.
		EnvironmentTypes().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, environmentTypeList)

	// Get Environment type list by catalog
	environmentTypeListByCatalog, err := projectClient.
		CatalogByName("SampleCatalog").
		EnvironmentDefinitions().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, environmentTypeListByCatalog)

	// Get Environment Definition List
	environmentDefinitionList, err := projectClient.
		EnvironmentDefinitions().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, environmentDefinitionList)

	// Get environment list
	environmentList, err := projectClient.
		Environments().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, environmentList)

	// Get environments by user
	userEnvironmentList, err := projectClient.
		EnvironmentsByMe().
		Get(*mockContext.Context)

	require.NoError(t, err)
	require.NotEmpty(t, userEnvironmentList)

	// Create environment
	envSpec := EnvironmentSpec{
		CatalogName:               "SampleCatalog",
		EnvironmentDefinitionName: "Sandbox",
		EnvironmentType:           "Dev",
	}

	envName := fmt.Sprintf("env-%d", time.Now().Unix())

	err = projectClient.
		EnvironmentByName(envName).
		Put(*mockContext.Context, envSpec)

	require.NoError(t, err)

	// Delete environment
	err = projectClient.
		EnvironmentByName(envName).
		Delete(*mockContext.Context)

	require.NoError(t, err)

	err = projectClient.
		EnvironmentByName(envName).
		Put(*mockContext.Context, envSpec)

	require.NoError(t, err)

	// Delete environment
	err = projectClient.
		EnvironmentByName(envName).
		Delete(*mockContext.Context)

	require.NoError(t, err)
}
