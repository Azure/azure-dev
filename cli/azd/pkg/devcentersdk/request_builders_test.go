// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package devcentersdk_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockdevcentersdk"
	"github.com/stretchr/testify/require"
)

var testDevCenters = []*devcentersdk.DevCenter{
	{
		//nolint:lll
		Id:             "/subscriptions/SUB_01/resourceGroups/RG_01/providers/Microsoft.DevCenter/devcenters/DevCenter1",
		SubscriptionId: "SUB_01",
		ResourceGroup:  "RG_01",
		Name:           "DevCenter1",
		ServiceUri:     "https://devcenter1.eastus.devcenter.azure.com",
	},
	{
		//nolint:lll
		Id:             "/subscriptions/SUB_02/resourceGroups/RG_02/providers/Microsoft.DevCenter/devcenters/DevCenter2",
		SubscriptionId: "SUB_02",
		ResourceGroup:  "RG_02",
		Name:           "DevCenter2",
		ServiceUri:     "https://devcenter2.eastus.devcenter.azure.com",
	},
}

func newTestClient(t *testing.T, mockContext *mocks.MockContext) devcentersdk.DevCenterClient {
	t.Helper()

	resourceGraphClient, err := armresourcegraph.NewClient(mockContext.Credentials, mockContext.ArmClientOptions)
	require.NoError(t, err)

	client, err := devcentersdk.NewDevCenterClient(
		mockContext.Credentials,
		mockContext.CoreClientOptions,
		resourceGraphClient,
		cloud.AzurePublic(),
	)
	require.NoError(t, err)
	require.NotNil(t, client)

	return client
}

func TestDevCenters_List(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	client := newTestClient(t, mockContext)

	list, err := client.DevCenters().Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, list)
	require.Len(t, list.Value, 2)
}

func TestDevCenterByEndpoint_And_ByName(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	client := newTestClient(t, mockContext)

	byEndpoint := client.DevCenterByEndpoint("https://devcenter1.eastus.devcenter.azure.com")
	require.NotNil(t, byEndpoint)
	require.NotNil(t, byEndpoint.Projects())
	require.NotNil(t, byEndpoint.ProjectByName("Project1"))

	byName := client.DevCenterByName("DevCenter1")
	require.NotNil(t, byName)
	require.NotNil(t, byName.Projects())
	require.NotNil(t, byName.ProjectByName("Project1"))
}

func TestProjects_List(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	client := newTestClient(t, mockContext)

	list, err := client.
		DevCenterByName("DevCenter1").
		Projects().
		Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, list)
	require.Len(t, list.Value, 1)
	require.Equal(t, "Project1", list.Value[0].Name)
}

func TestProject_Get(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.Path == "/projects/Project1"
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, map[string]any{"name": "Project1"})
	})

	client := newTestClient(t, mockContext)

	project, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		Get(context.Background())

	require.NoError(t, err)
	require.NotNil(t, project)
	require.Equal(t, "Project1", project.Name)
}

func TestProject_Get_NotFound(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.Path == "/projects/Missing"
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
	})

	client := newTestClient(t, mockContext)

	_, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Missing").
		Get(context.Background())
	require.Error(t, err)
}

func TestProject_Get_UnknownDevCenter(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	client := newTestClient(t, mockContext)

	_, err := client.
		DevCenterByName("UnknownDevCenter").
		ProjectByName("Project1").
		Get(context.Background())
	require.Error(t, err)
}

func TestCatalogs_List(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	catalogs := []*devcentersdk.Catalog{
		{Name: "Catalog1"},
		{Name: "Catalog2"},
	}
	mockdevcentersdk.MockListCatalogs(mockContext, "Project1", catalogs)

	client := newTestClient(t, mockContext)

	list, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		Catalogs().
		Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, list)
	require.Len(t, list.Value, 2)
}

func TestCatalog_Get(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.Path == "/projects/Project1/catalogs/Catalog1"
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, &devcentersdk.Catalog{Name: "Catalog1"})
	})

	client := newTestClient(t, mockContext)

	catalog, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		CatalogByName("Catalog1").
		Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Catalog1", catalog.Name)
}

func TestCatalog_Get_MalformedJSON(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.Path == "/projects/Project1/catalogs/Catalog1"
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		// Valid 200 status but payload that does not unmarshal into Catalog
		// struct (top-level string) forces ReadRawResponse to return an error.
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, "not-a-catalog-object")
	})

	client := newTestClient(t, mockContext)

	_, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		CatalogByName("Catalog1").
		Get(context.Background())
	require.Error(t, err)
}

func TestEnvironmentTypes_List(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	envTypes := []*devcentersdk.EnvironmentType{
		{Name: "Dev", DeploymentTargetId: "/subscriptions/SUB_01/", Status: "Enabled"},
		{Name: "Prod", DeploymentTargetId: "/subscriptions/SUB_01/", Status: "Enabled"},
	}
	mockdevcentersdk.MockListEnvironmentTypes(mockContext, "Project1", envTypes)

	client := newTestClient(t, mockContext)

	list, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		EnvironmentTypes().
		Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, list)
	require.Len(t, list.Value, 2)
}

func TestEnvironmentDefinitions_ListByProject(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	defs := []*devcentersdk.EnvironmentDefinition{
		{Name: "WebApp", CatalogName: "Catalog1"},
		{Name: "ContainerApp", CatalogName: "Catalog1"},
	}
	mockdevcentersdk.MockListEnvironmentDefinitions(mockContext, "Project1", defs)

	client := newTestClient(t, mockContext)

	list, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		EnvironmentDefinitions().
		Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, list)
	require.Len(t, list.Value, 2)
}

func TestEnvironmentDefinitions_ListByCatalog(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			req.URL.Path == "/projects/Project1/catalogs/Catalog1/environmentDefinitions"
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, devcentersdk.EnvironmentDefinitionListResponse{
			Value: []*devcentersdk.EnvironmentDefinition{
				{Name: "WebApp", CatalogName: "Catalog1"},
			},
		})
	})

	client := newTestClient(t, mockContext)

	list, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		CatalogByName("Catalog1").
		EnvironmentDefinitions().
		Get(context.Background())
	require.NoError(t, err)
	require.Len(t, list.Value, 1)
}

func TestEnvironmentDefinition_Get(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockdevcentersdk.MockGetEnvironmentDefinition(mockContext, "Project1", "Catalog1", "WebApp",
		&devcentersdk.EnvironmentDefinition{Name: "WebApp", CatalogName: "Catalog1"})

	client := newTestClient(t, mockContext)

	def, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		CatalogByName("Catalog1").
		EnvironmentDefinitionByName("WebApp").
		Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "WebApp", def.Name)
}

func TestEnvironments_List(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	envs := []*devcentersdk.Environment{
		{Name: "env1", ProvisioningState: devcentersdk.ProvisioningStateSucceeded},
		{Name: "env2", ProvisioningState: devcentersdk.ProvisioningStateSucceeded},
	}
	mockdevcentersdk.MockListEnvironmentsByProject(mockContext, "Project1", envs)

	client := newTestClient(t, mockContext)

	list, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		Environments().
		Get(context.Background())
	require.NoError(t, err)
	require.Len(t, list.Value, 2)
}

func TestEnvironments_ListByUser(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	envs := []*devcentersdk.Environment{
		{Name: "env1"},
	}
	mockdevcentersdk.MockListEnvironmentsByUser(mockContext, "Project1", "me", envs)

	client := newTestClient(t, mockContext)

	list, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		EnvironmentsByMe().
		Get(context.Background())
	require.NoError(t, err)
	require.Len(t, list.Value, 1)
}

func TestEnvironments_ListByExplicitUser(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockdevcentersdk.MockListEnvironmentsByUser(mockContext, "Project1", "user01",
		[]*devcentersdk.Environment{{Name: "user01-env"}})

	client := newTestClient(t, mockContext)

	list, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		EnvironmentsByUser("user01").
		Get(context.Background())
	require.NoError(t, err)
	require.Len(t, list.Value, 1)
}

func TestEnvironment_Get(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockdevcentersdk.MockGetEnvironment(mockContext, "Project1", "me", "env1",
		&devcentersdk.Environment{Name: "env1", ProvisioningState: devcentersdk.ProvisioningStateSucceeded})

	client := newTestClient(t, mockContext)

	env, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		EnvironmentsByMe().
		EnvironmentByName("env1").
		Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "env1", env.Name)
}

func TestEnvironment_Get_NotFound(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockdevcentersdk.MockGetEnvironment(mockContext, "Project1", "me", "missing", nil)

	client := newTestClient(t, mockContext)

	_, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		EnvironmentsByMe().
		EnvironmentByName("missing").
		Get(context.Background())
	require.Error(t, err)
}

func TestOutputs_Get(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			req.URL.Path == "/projects/Project1/users/me/environments/env1/outputs"
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, devcentersdk.OutputListResponse{
			Outputs: map[string]devcentersdk.OutputParameter{
				"webEndpoint": {
					Type:  devcentersdk.OutputParameterTypeString,
					Value: "https://example.com",
				},
			},
		})
	})

	client := newTestClient(t, mockContext)

	outputs, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		EnvironmentsByMe().
		EnvironmentByName("env1").
		Outputs().
		Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, outputs)
	require.Len(t, outputs.Outputs, 1)
	require.Equal(t, "https://example.com", outputs.Outputs["webEndpoint"].Value)
}

func TestOutputs_Get_Error(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			req.URL.Path == "/projects/Project1/users/me/environments/env1/outputs"
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusInternalServerError)
	})

	client := newTestClient(t, mockContext)

	_, err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		EnvironmentsByMe().
		EnvironmentByName("env1").
		Outputs().
		Get(context.Background())
	require.Error(t, err)
}

func TestEnvironment_Delete_Success(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockdevcentersdk.MockDeleteEnvironment(mockContext, "Project1", "me", "env1",
		&devcentersdk.OperationStatus{Status: "Succeeded"})

	client := newTestClient(t, mockContext)

	err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		EnvironmentsByMe().
		EnvironmentByName("env1").
		Delete(context.Background())
	require.NoError(t, err)
}

func TestEnvironment_Put_Success(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	mockdevcentersdk.MockPutEnvironment(mockContext, "Project1", "me", "env1",
		&devcentersdk.OperationStatus{Status: "Succeeded"})

	client := newTestClient(t, mockContext)

	spec := devcentersdk.EnvironmentSpec{
		CatalogName:               "Catalog1",
		EnvironmentDefinitionName: "WebApp",
		EnvironmentType:           "Dev",
		Parameters: map[string]any{
			"environmentName": "env1",
		},
	}

	err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		EnvironmentsByMe().
		EnvironmentByName("env1").
		Put(context.Background(), spec)
	require.NoError(t, err)
}

func TestEnvironment_Put_Error(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	// Status != "Succeeded" causes MockPutEnvironment to return 400
	mockdevcentersdk.MockPutEnvironment(mockContext, "Project1", "me", "env1",
		&devcentersdk.OperationStatus{Status: "Failed"})

	client := newTestClient(t, mockContext)

	spec := devcentersdk.EnvironmentSpec{
		CatalogName:               "Catalog1",
		EnvironmentDefinitionName: "WebApp",
		EnvironmentType:           "Dev",
	}

	err := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		EnvironmentsByMe().
		EnvironmentByName("env1").
		Put(context.Background(), spec)
	require.Error(t, err)
}

func TestList_FilterAndTop_FluentMethods(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	// Capture the outgoing request to verify $filter and $top query parameters.
	var capturedURL string
	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			req.URL.Path == "/projects/Project1/environments"
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		capturedURL = req.URL.String()
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			devcentersdk.EnvironmentListResponse{Value: []*devcentersdk.Environment{}})
	})

	client := newTestClient(t, mockContext)

	builder := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		Environments()
	builder.Filter("name eq 'env1'")
	builder.Top(5)

	_, err := builder.Get(context.Background())
	require.NoError(t, err)
	require.Contains(t, capturedURL, "%24filter=name+eq+%27env1%27")
	require.Contains(t, capturedURL, "%24top=5")
}

func TestItem_SelectFluentMethod(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockdevcentersdk.MockDevCenterGraphQuery(mockContext, testDevCenters)

	var capturedURL string
	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.Path == "/projects/Project1/catalogs/Catalog1"
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		capturedURL = req.URL.String()
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, &devcentersdk.Catalog{Name: "Catalog1"})
	})

	client := newTestClient(t, mockContext)

	builder := client.
		DevCenterByName("DevCenter1").
		ProjectByName("Project1").
		CatalogByName("Catalog1")
	builder.Select([]string{"name", "description"})

	_, err := builder.Get(context.Background())
	require.NoError(t, err)
	require.Contains(t, capturedURL, "%24select=name%2Cdescription")
}

func TestDevCenters_List_GraphError(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	// Respond with a 500 to the resource graph call to exercise the error path
	mockContext.HttpClient.When(func(req *http.Request) bool {
		return strings.Contains(req.URL.Path, "Microsoft.ResourceGraph/resources")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusInternalServerError)
	})

	client := newTestClient(t, mockContext)

	_, err := client.DevCenters().Get(context.Background())
	require.Error(t, err)
}
