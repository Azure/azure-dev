// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azsdk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestZipDeploy(t *testing.T) {
	t.Run("WithPollingSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
				Progress:   new("Running ORYX build"),
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

// scmTransportFunc adapts a function to policy.Transporter for testing.
type scmTransportFunc func(*http.Request) (*http.Response, error)

func (f scmTransportFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

// scmTimeoutError satisfies net.Error with Timeout() returning true.
type scmTimeoutError struct{ msg string }

var _ net.Error = (*scmTimeoutError)(nil)

func (e *scmTimeoutError) Error() string   { return e.msg }
func (e *scmTimeoutError) Timeout() bool   { return true }
func (e *scmTimeoutError) Temporary() bool { return false }

func newTestScmClient(
	transport policy.Transporter,
) *ZipDeployClient {
	pipeline := runtime.NewPipeline(
		"test", "1.0.0",
		runtime.PipelineOptions{},
		&policy.ClientOptions{
			Transport: transport,
			Retry: policy.RetryOptions{
				MaxRetries:    1,
				RetryDelay:    time.Nanosecond,
				MaxRetryDelay: time.Nanosecond,
			},
		},
	)
	return &ZipDeployClient{
		hostName: "test.scm.azurewebsites.net",
		pipeline: pipeline,
	}
}

func TestIsScmReady(t *testing.T) {
	tests := []struct {
		name            string
		transport       scmTransportFunc
		ctx             func(*testing.T) context.Context
		wantReady       bool
		wantErr         error
		wantErrContains string
	}{
		{
			name: "HTTP200_Ready",
			transport: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{},
					Request:    req,
					Body:       http.NoBody,
				}, nil
			},
			wantReady: true,
		},
		{
			name: "HTTP502_BadGateway",
			transport: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Header:     http.Header{},
					Request:    req,
					Body:       http.NoBody,
				}, nil
			},
			wantReady: false,
		},
		{
			name: "HTTP503_ServiceUnavailable",
			transport: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Header:     http.Header{},
					Request:    req,
					Body:       http.NoBody,
				}, nil
			},
			wantReady: false,
		},
		{
			name: "HTTP500_InternalServerError",
			transport: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Header:     http.Header{},
					Request:    req,
					Body:       http.NoBody,
				}, nil
			},
			wantReady:       false,
			wantErrContains: "500",
		},
		{
			name: "ConnectionRefused",
			transport: func(req *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("dial tcp: connection refused")
			},
			wantReady: false,
		},
		{
			name: "NoSuchHost",
			transport: func(req *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf(
					"dial tcp: lookup host: no such host",
				)
			},
			wantReady: false,
		},
		{
			name: "NetTimeout",
			transport: func(req *http.Request) (*http.Response, error) {
				return nil, &scmTimeoutError{msg: "i/o timeout"}
			},
			wantReady: false,
		},
		{
			name: "ContextCanceled",
			transport: func(req *http.Request) (*http.Response, error) {
				return nil, context.Canceled
			},
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			wantReady: false,
			wantErr:   context.Canceled,
		},
		{
			name: "ContextDeadlineExceeded",
			transport: func(req *http.Request) (*http.Response, error) {
				return nil, context.DeadlineExceeded
			},
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithDeadline(
					t.Context(),
					time.Now().Add(-time.Second),
				)
				t.Cleanup(cancel)
				return ctx
			},
			wantReady: false,
			wantErr:   context.DeadlineExceeded,
		},
		{
			name: "UnknownTransportError_TLS",
			transport: func(req *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("tls: handshake failure")
			},
			wantReady:       false,
			wantErrContains: "SCM readiness probe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestScmClient(tt.transport)

			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(t)
			}

			ready, err := client.IsScmReady(ctx)
			require.Equal(t, tt.wantReady, ready)

			switch {
			case tt.wantErr != nil:
				require.ErrorIs(t, err, tt.wantErr)
			case tt.wantErrContains != "":
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrContains)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestLogWebAppDeploymentStatus(t *testing.T) {
	noop := func(string) {}

	t.Run("RuntimeStartingWithZeroInstances", func(t *testing.T) {
		res := armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse{
			CsmDeploymentStatus: armappservice.CsmDeploymentStatus{
				Properties: &armappservice.CsmDeploymentStatusProperties{
					Status:                      to.Ptr(armappservice.DeploymentBuildStatusRuntimeStarting),
					NumberOfInstancesInProgress: new(int32),
					NumberOfInstancesSuccessful: new(int32),
					NumberOfInstancesFailed:     new(int32),
				},
			},
		}

		result := logWebAppDeploymentStatus(res, "", noop)
		require.NoError(t, result.err)
	})

	t.Run("RuntimeStartingWithInstances", func(t *testing.T) {
		res := armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse{
			CsmDeploymentStatus: armappservice.CsmDeploymentStatus{
				Properties: &armappservice.CsmDeploymentStatusProperties{
					Status:                      to.Ptr(armappservice.DeploymentBuildStatusRuntimeStarting),
					NumberOfInstancesInProgress: new(int32(1)),
					NumberOfInstancesSuccessful: new(int32),
					NumberOfInstancesFailed:     new(int32),
				},
			},
		}

		result := logWebAppDeploymentStatus(res, "", noop)
		require.NoError(t, result.err)
	})

	t.Run("RuntimeSuccessful", func(t *testing.T) {
		res := armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse{
			CsmDeploymentStatus: armappservice.CsmDeploymentStatus{
				Properties: &armappservice.CsmDeploymentStatusProperties{
					Status:                      to.Ptr(armappservice.DeploymentBuildStatusRuntimeSuccessful),
					NumberOfInstancesInProgress: new(int32),
					NumberOfInstancesSuccessful: new(int32(1)),
					NumberOfInstancesFailed:     new(int32),
				},
			},
		}

		result := logWebAppDeploymentStatus(res, "", noop)
		require.NoError(t, result.err)
	})

	t.Run("RuntimeFailed", func(t *testing.T) {
		res := armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse{
			CsmDeploymentStatus: armappservice.CsmDeploymentStatus{
				Properties: &armappservice.CsmDeploymentStatusProperties{
					Status:                      to.Ptr(armappservice.DeploymentBuildStatusRuntimeFailed),
					NumberOfInstancesInProgress: new(int32),
					NumberOfInstancesSuccessful: new(int32),
					NumberOfInstancesFailed:     new(int32(1)),
				},
			},
		}

		result := logWebAppDeploymentStatus(res, "", noop)
		require.Error(t, result.err)
	})

	t.Run("EmptyResponse", func(t *testing.T) {
		res := armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse{}

		result := logWebAppDeploymentStatus(res, "", noop)
		require.Error(t, result.err)
		require.Contains(t, result.err.Error(), "response or its properties are empty")
	})

	t.Run("NilStatus", func(t *testing.T) {
		res := armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse{
			CsmDeploymentStatus: armappservice.CsmDeploymentStatus{
				Properties: &armappservice.CsmDeploymentStatusProperties{
					Status: nil,
				},
			},
		}

		result := logWebAppDeploymentStatus(res, "", noop)
		require.Error(t, result.err)
		require.Contains(t, result.err.Error(), "response or its properties are empty")
	})

	t.Run("NilInstanceCounters", func(t *testing.T) {
		// When instance counters are nil, should not panic
		res := armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse{
			CsmDeploymentStatus: armappservice.CsmDeploymentStatus{
				Properties: &armappservice.CsmDeploymentStatusProperties{
					Status:                      to.Ptr(armappservice.DeploymentBuildStatusRuntimeStarting),
					NumberOfInstancesInProgress: nil,
					NumberOfInstancesSuccessful: nil,
					NumberOfInstancesFailed:     nil,
				},
			},
		}

		result := logWebAppDeploymentStatus(res, "", noop)
		require.NoError(t, result.err)
	})
}

func TestDeployTrackStatus_StatusTrackingTimeout(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	registerTrackedDeployMocks(mockContext)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "/deploymentStatus/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response, err := mocks.CreateHttpResponseWithBody(
			request,
			http.StatusAccepted,
			map[string]any{
				"status": "InProgress",
				"properties": map[string]any{
					"status":                      armappservice.DeploymentBuildStatusBuildInProgress,
					"numberOfInstancesSuccessful": 0,
					"numberOfInstancesFailed":     0,
					"numberOfInstancesInProgress": 1,
				},
			},
		)
		response.Header.Set("Azure-AsyncOperation", request.URL.String())
		return response, err
	})

	client, err := NewZipDeployClient("HOSTNAME", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
	require.NoError(t, err)

	err = client.deployTrackStatus(
		*mockContext.Context,
		bytes.NewReader(nil),
		"SUBSCRIPTION_ID",
		"RESOURCE_GROUP_ID",
		"APP_NAME",
		time.Millisecond,
		time.Millisecond,
		func(string) {},
	)

	timeoutErr, ok := errors.AsType[*DeploymentStatusTimeoutError](err)
	require.True(t, ok)
	require.Equal(t, time.Millisecond, timeoutErr.Timeout)
}

func TestDeployTrackStatus_InitialStatusRequestTimeout(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	registerTrackedDeployMocks(mockContext)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "/deploymentStatus/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})

	client, err := NewZipDeployClient("HOSTNAME", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
	require.NoError(t, err)

	err = client.deployTrackStatus(
		*mockContext.Context,
		bytes.NewReader(nil),
		"SUBSCRIPTION_ID",
		"RESOURCE_GROUP_ID",
		"APP_NAME",
		time.Millisecond,
		time.Millisecond,
		func(string) {},
	)

	timeoutErr, ok := errors.AsType[*DeploymentStatusTimeoutError](err)
	require.True(t, ok)
	require.Equal(t, time.Millisecond, timeoutErr.Timeout)
}

func TestDeployTrackStatus_StatusChangeResetsTimeout(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	registerTrackedDeployMocks(mockContext)

	pollCount := 0
	polledAfterStatusChange := false
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "/deploymentStatus/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		pollCount++
		status := armappservice.DeploymentBuildStatusBuildInProgress
		if pollCount > 1 {
			if pollCount == 2 {
				// Leave less than one poll interval on the original deadline. A reset
				// is required for the next request to occur.
				time.Sleep(250 * time.Millisecond)
			} else {
				polledAfterStatusChange = true
			}
			status = armappservice.DeploymentBuildStatusRuntimeStarting
		}

		response, err := mocks.CreateHttpResponseWithBody(
			request,
			http.StatusAccepted,
			map[string]any{
				"status": "InProgress",
				"properties": map[string]any{
					"status":                      status,
					"numberOfInstancesSuccessful": 0,
					"numberOfInstancesFailed":     0,
					"numberOfInstancesInProgress": 1,
				},
			},
		)
		response.Header.Set("Azure-AsyncOperation", request.URL.String())
		return response, err
	})

	client, err := NewZipDeployClient("HOSTNAME", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
	require.NoError(t, err)

	err = client.deployTrackStatus(
		*mockContext.Context,
		bytes.NewReader(nil),
		"SUBSCRIPTION_ID",
		"RESOURCE_GROUP_ID",
		"APP_NAME",
		750*time.Millisecond,
		500*time.Millisecond,
		func(string) {},
	)

	timeoutErr, ok := errors.AsType[*DeploymentStatusTimeoutError](err)
	require.True(t, ok)
	require.Equal(t, 750*time.Millisecond, timeoutErr.Timeout)
	require.True(t, polledAfterStatusChange, "expected another status poll after the timeout reset")
}

func registerTrackedDeployMocks(mockContext *mocks.MockContext) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "/api/zipdeploy")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response, _ := mocks.CreateEmptyHttpResponse(request, http.StatusAccepted)
		response.Header.Set("Scm-Deployment-Id", "00000000-0000-0000-0000-000000000000")
		return response, nil
	})
}
