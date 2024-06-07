package azsdk

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestZipDeploy(t *testing.T) {
	t.Run("WithPollingSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerDeployMocks(mockContext)
		registerPollingMocks(mockContext)

		client, err := NewZipDeployClient("HOSTNAME", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
		require.NoError(t, err)

		zipFile := bytes.NewReader([]byte{})
		poller, err := client.BeginDeploy(*mockContext.Context, zipFile)
		require.NotNil(t, poller)
		require.NoError(t, err)

		response, err := poller.PollUntilDone(*mockContext.Context, &runtime.PollUntilDoneOptions{
			Frequency: 250 * time.Millisecond,
		})

		require.NoError(t, err)
		require.True(t, response.Complete)
	})

	t.Run("WithPollingError", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerDeployMocks(mockContext)
		registerPollingErrorMocks(mockContext)

		client, err := NewZipDeployClient("HOSTNAME", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
		require.NoError(t, err)

		zipFile := bytes.NewReader([]byte{})
		poller, err := client.BeginDeploy(*mockContext.Context, zipFile)
		require.NotNil(t, poller)
		require.NoError(t, err)

		response, err := poller.PollUntilDone(*mockContext.Context, &runtime.PollUntilDoneOptions{
			Frequency: 250 * time.Millisecond,
		})

		require.Nil(t, response)
		require.Error(t, err)
	})

	t.Run("WithInitialError", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerConflictMocks(mockContext)

		client, err := NewZipDeployClient("HOSTNAME", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
		require.NoError(t, err)

		zipFile := bytes.NewReader([]byte{})
		poller, err := client.BeginDeploy(*mockContext.Context, zipFile)
		require.Nil(t, poller)
		require.Error(t, err)
	})
}

func registerConflictMocks(mockContext *mocks.MockContext) {
	// Original call to start the deployment operation
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "/api/zipdeploy")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, http.StatusConflict)
	})
}

func registerDeployMocks(mockContext *mocks.MockContext) {
	// Original call to start the deployment operation
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "/api/zipdeploy")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response, _ := mocks.CreateEmptyHttpResponse(request, http.StatusAccepted)
		response.Header.Set("Location", "https://myapp.scm.azurewebsites.net/deployments/latest")

		return response, nil
	})
}
func registerPollingMocks(mockContext *mocks.MockContext) {
	pollCount := 0

	// Polling call to check on the deployment status
	mockContext.HttpClient.When(func(request *http.Request) bool {
		pollCount += 1
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/deployments/latest")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		acceptedStatus := DeployStatusResponse{
			DeployStatus: DeployStatus{
				Id:         "ID",
				Status:     http.StatusAccepted,
				StatusText: "Accepted",
				Message:    "Doing deploy things",
				Progress:   convert.RefOf("Running ORYX build"),
				Complete:   false,
				Active:     false,
				SiteName:   "APP_NAME",
			},
		}

		completeStatus := DeployStatusResponse{
			DeployStatus: DeployStatus{
				Id:         "ID",
				Status:     http.StatusOK,
				StatusText: "OK",
				Message:    "Deployment Complete",
				Progress:   nil,
				Complete:   true,
				Active:     true,
				SiteName:   "APP_NAME",
				LogUrl:     "https://log.url",
			},
		}

		var statusCode int
		var response any

		if pollCount >= 3 {
			statusCode = http.StatusOK
			response = completeStatus
		} else {
			statusCode = http.StatusAccepted
			response = acceptedStatus
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, response)
	})

}

func registerPollingErrorMocks(mockContext *mocks.MockContext) {
	// Polling call to check on the deployment status
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/deployments/latest")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		errorStatus := DeployStatusResponse{
			DeployStatus: DeployStatus{
				Id:         "ID",
				Status:     http.StatusBadRequest,
				StatusText: "Error",
				Message:    "Bad deploy package",
				Progress:   nil,
				Complete:   true,
				Active:     false,
				SiteName:   "APP_NAME",
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusBadRequest, errorStatus)
	})
}
