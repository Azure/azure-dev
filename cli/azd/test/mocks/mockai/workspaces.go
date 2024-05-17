package mockai

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func RegisterGetWorkspaceMock(mockContext *mocks.MockContext, workspaceName string) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			fmt.Sprintf(
				"providers/Microsoft.MachineLearningServices/workspaces/%s",
				workspaceName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := armmachinelearning.WorkspacesClientGetResponse{
			Workspace: armmachinelearning.Workspace{
				Name:     &workspaceName,
				ID:       convert.RefOf("ID"),
				Location: convert.RefOf("eastus2"),
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}
