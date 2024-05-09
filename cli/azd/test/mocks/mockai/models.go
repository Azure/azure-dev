package mockai

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func RegisterGetModel(mockContext *mocks.MockContext, workspaceName string, modelName string, statusCode int) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			fmt.Sprintf(
				"providers/Microsoft.MachineLearningServices/workspaces/%s/models/%s",
				workspaceName,
				modelName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		if statusCode == http.StatusNotFound {
			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		}

		response := armmachinelearning.ModelContainersClientGetResponse{
			ModelContainer: armmachinelearning.ModelContainer{
				Name: &modelName,
				Properties: &armmachinelearning.ModelContainerProperties{
					LatestVersion: convert.RefOf("2"),
					NextVersion:   convert.RefOf("3"),
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func RegisterGetModelVersion(
	mockContext *mocks.MockContext,
	workspaceName string,
	modelName string,
	version string,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			fmt.Sprintf(
				"providers/Microsoft.MachineLearningServices/workspaces/%s/models/%s/versions/%s",
				workspaceName,
				modelName,
				version,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := armmachinelearning.ModelVersionsClientGetResponse{
			ModelVersion: armmachinelearning.ModelVersion{
				Name: convert.RefOf(fmt.Sprint(version)),
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}
