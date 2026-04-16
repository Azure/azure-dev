// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azsdk

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
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
