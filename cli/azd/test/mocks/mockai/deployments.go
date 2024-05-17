package mockai

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func RegisterGetOnlineDeployment(
	mockContext *mocks.MockContext,
	workspaceName string,
	endpointName string,
	deploymentName string,
	provisioningState armmachinelearning.DeploymentProvisioningState,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			fmt.Sprintf(
				"providers/Microsoft.MachineLearningServices/workspaces/%s/onlineEndpoints/%s/deployments/%s",
				workspaceName,
				endpointName,
				deploymentName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := armmachinelearning.OnlineDeploymentsClientGetResponse{
			OnlineDeployment: armmachinelearning.OnlineDeployment{
				Name: &deploymentName,
				Properties: &armmachinelearning.OnlineDeploymentProperties{
					ProvisioningState: &provisioningState,
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func RegisterDeleteOnlineDeployment(
	mockContext *mocks.MockContext,
	workspaceName string,
	endpointName string,
	deploymentName string,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete && strings.HasSuffix(
			request.URL.Path,
			fmt.Sprintf(
				"providers/Microsoft.MachineLearningServices/workspaces/%s/onlineEndpoints/%s/deployments/%s",
				workspaceName,
				endpointName,
				deploymentName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := armmachinelearning.OnlineDeploymentsClientDeleteResponse{}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func RegisterListOnlineDeployment(
	mockContext *mocks.MockContext,
	workspaceName string,
	endpointName string,
	deploymentNames []string,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			fmt.Sprintf(
				"providers/Microsoft.MachineLearningServices/workspaces/%s/onlineEndpoints/%s/deployments",
				workspaceName,
				endpointName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		deployments := []*armmachinelearning.OnlineDeployment{}
		for _, deploymentName := range deploymentNames {
			deployments = append(deployments, &armmachinelearning.OnlineDeployment{
				Name: convert.RefOf(deploymentName),
			})
		}

		response := armmachinelearning.OnlineDeploymentsClientListResponse{
			OnlineDeploymentTrackedResourceArmPaginatedResult: armmachinelearning.OnlineDeploymentTrackedResourceArmPaginatedResult{
				Value: deployments,
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}
