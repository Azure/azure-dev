package mockai

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func RegisterGetEnvironment(
	mockContext *mocks.MockContext,
	workspaceName string,
	environmentName string,
	statusCode int,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			fmt.Sprintf(
				"providers/Microsoft.MachineLearningServices/workspaces/%s/environments/%s",
				workspaceName,
				environmentName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		if statusCode == http.StatusNotFound {
			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		}

		response := armmachinelearning.EnvironmentContainersClientGetResponse{
			EnvironmentContainer: armmachinelearning.EnvironmentContainer{
				Name: &environmentName,
				Properties: &armmachinelearning.EnvironmentContainerProperties{
					LatestVersion: convert.RefOf("2"),
					NextVersion:   convert.RefOf("3"),
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func RegisterGetEnvironmentVersion(
	mockContext *mocks.MockContext,
	workspaceName string,
	environmentName string,
	version string,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			fmt.Sprintf(
				"providers/Microsoft.MachineLearningServices/workspaces/%s/environments/%s/versions/%s",
				workspaceName,
				environmentName,
				version,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := armmachinelearning.EnvironmentVersionsClientGetResponse{
			EnvironmentVersion: armmachinelearning.EnvironmentVersion{
				Name: convert.RefOf(fmt.Sprint(version)),
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}
