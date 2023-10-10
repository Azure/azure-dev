package mockdevcentersdk

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func MockDevCenterGraphQuery(mockContext *mocks.MockContext, devCenters []*devcentersdk.DevCenter) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.Contains(request.URL.Path, "providers/Microsoft.ResourceGraph/resources")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		resources := []*devcentersdk.GenericResource{}
		for _, devCenter := range devCenters {
			resources = append(resources, &devcentersdk.GenericResource{
				Id: fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DevCenter/projects/Project1",
					devCenter.SubscriptionId,
					devCenter.ResourceGroup,
				),
				Location: "eastus2",
				Name:     "Project1",
				Type:     "microsoft.devcenter/projects",
				TenantId: "TENANT_ID",
				Properties: map[string]interface{}{
					"devCenterUri": devCenter.ServiceUri,
					"devCenterId":  devCenter.Id,
				},
			})
		}

		body := armresourcegraph.ClientResourcesResponse{
			QueryResponse: armresourcegraph.QueryResponse{
				Data: resources,
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, body)
	})
}

func MockListEnvironmentsByProject(
	mockContext *mocks.MockContext,
	projectName string,
	environments []*devcentersdk.Environment,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			request.URL.Path == fmt.Sprintf("/projects/%s/environments", projectName)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := devcentersdk.EnvironmentListResponse{
			Value: environments,
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func MockListEnvironmentsByUser(
	mockContext *mocks.MockContext,
	projectName string,
	userId string,
	environments []*devcentersdk.Environment,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			request.URL.Path == fmt.Sprintf("/projects/%s/users/%s/environments", projectName, userId)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := devcentersdk.EnvironmentListResponse{
			Value: environments,
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func MockGetEnvironment(
	mockContext *mocks.MockContext,
	projectName string,
	userId string,
	environmentName string,
	environment *devcentersdk.Environment,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			request.URL.Path == fmt.Sprintf(
				"/projects/%s/users/%s/environments/%s",
				projectName,
				userId,
				environmentName,
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := environment

		if environment == nil {
			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func MockListEnvironmentDefinitions(
	mockContext *mocks.MockContext,
	projectName string,
	environmentDefinitions []*devcentersdk.EnvironmentDefinition,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			request.URL.Path == fmt.Sprintf("/projects/%s/environmentDefinitions", projectName)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := devcentersdk.EnvironmentDefinitionListResponse{
			Value: environmentDefinitions,
		}

		if environmentDefinitions == nil {
			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func MockListCatalogs(
	mockContext *mocks.MockContext,
	projectName string,
	catalogs []*devcentersdk.Catalog,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			request.URL.Path == fmt.Sprintf("/projects/%s/catalogs", projectName)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := devcentersdk.CatalogListResponse{
			Value: catalogs,
		}

		if catalogs == nil {
			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func MockListEnvironmentTypes(
	mockContext *mocks.MockContext,
	projectName string,
	environmentTypes []*devcentersdk.EnvironmentType,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			request.URL.Path == fmt.Sprintf("/projects/%s/environmentTypes", projectName)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := devcentersdk.EnvironmentTypeListResponse{
			Value: environmentTypes,
		}

		if environmentTypes == nil {
			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}
