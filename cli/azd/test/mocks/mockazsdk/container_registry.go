package mockazsdk

import (
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func MockContainerRegistryTokenExchange(
	mockContext *mocks.MockContext,
	subscriptionId string,
	loginServer string,
	refreshToken string,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "oauth2/exchange")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := struct {
			RefreshToken string `json:"refresh_token"`
		}{
			RefreshToken: refreshToken,
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func MockContainerRegistryList(mockContext *mocks.MockContext, registries []*armcontainerregistry.Registry) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "Microsoft.ContainerRegistry/registries")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		result := armcontainerregistry.RegistryListResult{
			NextLink: nil,
			Value:    registries,
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, result)
	})

	return mockRequest
}

func MockContainerRegistryCredentials(
	mockContext *mocks.MockContext,
	credentials *armcontainerregistry.RegistryListCredentialsResult,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "listCredentials")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, credentials)
	})

	return mockRequest
}
