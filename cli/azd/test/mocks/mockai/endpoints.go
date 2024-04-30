package mockai

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func RegisterGetOnlineEndpoint(
	mockContext *mocks.MockContext,
	workspaceName string,
	endpointName string,
	statusCode int,
	endpoint *armmachinelearning.OnlineEndpoint,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			fmt.Sprintf(
				"providers/Microsoft.MachineLearningServices/workspaces/%s/onlineEndpoints/%s",
				workspaceName,
				endpointName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		if statusCode == http.StatusNotFound {
			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		}

		if endpoint == nil {
			endpoint = &armmachinelearning.OnlineEndpoint{
				Name: &endpointName,
				Properties: &armmachinelearning.OnlineEndpointProperties{
					Traffic: map[string]*int32{},
				},
			}
		}

		response := armmachinelearning.OnlineEndpointsClientGetResponse{
			OnlineEndpoint: *endpoint,
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}

func RegisterUpdateOnlineEndpoint(
	mockContext *mocks.MockContext,
	workspaceName string,
	endpointName string,
	trafficMap map[string]*int32,
) *http.Request {
	mockRequest := &http.Request{}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPut && strings.HasSuffix(
			request.URL.Path,
			fmt.Sprintf(
				"providers/Microsoft.MachineLearningServices/workspaces/%s/onlineEndpoints/%s",
				workspaceName,
				endpointName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*mockRequest = *request

		response := armmachinelearning.OnlineEndpointsClientCreateOrUpdateResponse{
			OnlineEndpoint: armmachinelearning.OnlineEndpoint{
				Name: &endpointName,
				Properties: &armmachinelearning.OnlineEndpointProperties{
					Traffic:           trafficMap,
					ProvisioningState: convert.RefOf(armmachinelearning.EndpointProvisioningStateSucceeded),
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	return mockRequest
}
