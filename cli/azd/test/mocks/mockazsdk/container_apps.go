package mockazsdk

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func MockContainerAppGet(
	mockContext *mocks.MockContext,
	subscriptionId string,
	resourceGroup string,
	appName string,
	containerApp *armappcontainers.ContainerApp,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
				subscriptionId,
				resourceGroup,
				appName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := armappcontainers.ContainerAppsClientGetResponse{
			ContainerApp: *containerApp,
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func MockContainerAppUpdate(
	mockContext *mocks.MockContext,
	subscriptionId string,
	resourceGroup string,
	appName string,
	containerApp *armappcontainers.ContainerApp,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPatch && strings.Contains(
			request.URL.Path,
			fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
				subscriptionId,
				resourceGroup,
				appName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := armappcontainers.ContainerAppsClientUpdateResponse{}

		return mocks.CreateHttpResponseWithBody(request, http.StatusAccepted, response)
	})

	return mockRequest
}

func MockContainerAppRevisionGet(
	mockContext *mocks.MockContext,
	subscriptionId string,
	resourceGroup string,
	appName string,
	revisionName string,
	revision *armappcontainers.Revision,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s/revisions/%s",
				subscriptionId,
				resourceGroup,
				appName,
				revisionName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := armappcontainers.ContainerAppsRevisionsClientGetRevisionResponse{
			Revision: *revision,
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func MockContainerAppSecretsList(
	mockContext *mocks.MockContext,
	subscriptionId string,
	resourceGroup string,
	appName string,
	secrets *armappcontainers.SecretsCollection,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(
			request.URL.Path,
			fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s/listSecrets",
				subscriptionId,
				resourceGroup,
				appName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := armappcontainers.ContainerAppsClientListSecretsResponse{
			SecretsCollection: *secrets,
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}
