// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azsdk

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// respondPublishPost registers a mock response for the POST /api/publish request.
func respondPublishPost(
	mockContext *mocks.MockContext,
	statusCode int,
	body string,
	capture func(*http.Request),
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "/api/publish")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if capture != nil {
			capture(request)
		}
		resp, err := mocks.CreateEmptyHttpResponse(request, statusCode)
		if err != nil {
			return nil, err
		}
		resp.Body = http.NoBody
		if body != "" {
			resp.Body = makeBody(body)
			resp.ContentLength = int64(len(body))
		}
		return resp, nil
	})
}

// respondDeploymentGet registers a mock response for GET /api/deployments/{id}.
func respondDeploymentGet(
	mockContext *mocks.MockContext,
	statusCode int,
	payload string,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/api/deployments/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		resp, err := mocks.CreateEmptyHttpResponse(request, statusCode)
		if err != nil {
			return nil, err
		}
		resp.Body = http.NoBody
		if payload != "" {
			resp.Body = makeBody(payload)
			resp.ContentLength = int64(len(payload))
		}
		return resp, nil
	})
}

func makeBody(s string) *readCloser {
	return &readCloser{Reader: strings.NewReader(s)}
}

type readCloser struct {
	*strings.Reader
}

func (r *readCloser) Close() error { return nil }

func TestNewFuncAppHostClient(t *testing.T) {
	t.Parallel()

	t.Run("NilOptions", func(t *testing.T) {
		t.Parallel()
		client, err := NewFuncAppHostClient("host.example", &mocks.MockCredentials{}, nil)
		require.NoError(t, err)
		require.NotNil(t, client)
		require.Equal(t, "host.example", client.hostName)
	})

	t.Run("WithOptions", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		client, err := NewFuncAppHostClient("host.example", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
		require.NoError(t, err)
		require.NotNil(t, client)
	})
}

func TestFuncAppHostClient_Publish_Success(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())

	var captured *http.Request
	respondPublishPost(mockContext, http.StatusAccepted, `"deploy-123"`, func(r *http.Request) {
		captured = r
	})
	respondDeploymentGet(mockContext, http.StatusOK, `{"id":"deploy-123","status":4,"complete":true,"site_name":"app"}`)

	client, err := NewFuncAppHostClient("host.example", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
	require.NoError(t, err)

	zip := bytes.NewReader([]byte("zip-data"))
	resp, err := client.Publish(*mockContext.Context, zip, nil)
	require.NoError(t, err)
	require.Equal(t, "deploy-123", resp.Id)
	require.Equal(t, PublishStatusSuccess, resp.Status)
	require.Equal(t, "app", resp.SiteName)

	require.NotNil(t, captured)
	require.Equal(t, "application/zip", captured.Header.Get("Content-Type"))
	require.Equal(t, "application/json", captured.Header.Get("Accept"))
	require.Empty(t, captured.URL.Query().Get("RemoteBuild"))
}

func TestFuncAppHostClient_Publish_RemoteBuildQueryParam(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())

	var captured *http.Request
	respondPublishPost(mockContext, http.StatusAccepted, `"id-1"`, func(r *http.Request) {
		captured = r
	})
	respondDeploymentGet(mockContext, http.StatusOK, `{"id":"id-1","status":4,"complete":true}`)

	client, err := NewFuncAppHostClient("host.example", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
	require.NoError(t, err)

	zip := bytes.NewReader([]byte{})
	_, err = client.Publish(*mockContext.Context, zip, &PublishOptions{RemoteBuild: true})
	require.NoError(t, err)
	require.NotNil(t, captured)
	require.Equal(t, "true", captured.URL.Query().Get("RemoteBuild"))
}

func TestFuncAppHostClient_Publish_PostErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		statusCode  int
		body        string
		wantErrText string
	}{
		{
			name:        "PostNonAcceptedStatus",
			statusCode:  http.StatusBadRequest,
			body:        `"bad"`,
			wantErrText: "",
		},
		{
			name:        "InvalidJsonBody",
			statusCode:  http.StatusAccepted,
			body:        `not-json`,
			wantErrText: "invalid character",
		},
		{
			name:        "EmptyDeploymentId",
			statusCode:  http.StatusAccepted,
			body:        `""`,
			wantErrText: "missing deployment id",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockContext := mocks.NewMockContext(t.Context())
			respondPublishPost(mockContext, tc.statusCode, tc.body, nil)

			client, err := NewFuncAppHostClient(
				"host.example", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
			require.NoError(t, err)

			zip := bytes.NewReader([]byte{})
			_, err = client.Publish(*mockContext.Context, zip, nil)
			require.Error(t, err)
			if tc.wantErrText != "" {
				require.Contains(t, err.Error(), tc.wantErrText)
			}
		})
	}
}

func TestFuncAppHostClient_WaitForDeployment_StatusHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		getStatus   int
		payload     string
		wantErrText string
		wantOk      bool
	}{
		{
			name:      "SuccessStatus",
			getStatus: http.StatusOK,
			payload:   `{"id":"d1","status":4,"complete":true}`,
			wantOk:    true,
		},
		{
			name:        "CancelledStatus",
			getStatus:   http.StatusOK,
			payload:     `{"id":"d1","status":-1}`,
			wantErrText: "deployment was cancelled",
		},
		{
			name:        "FailedStatus",
			getStatus:   http.StatusOK,
			payload:     `{"id":"d1","status":3,"status_text":"boom"}`,
			wantErrText: "deployment failed: boom",
		},
		{
			name:        "ConflictStatus",
			getStatus:   http.StatusOK,
			payload:     `{"id":"d1","status":5}`,
			wantErrText: "another deployment being in progress",
		},
		{
			name:        "PartialSuccessStatus",
			getStatus:   http.StatusOK,
			payload:     `{"id":"d1","status":6,"status_text":"some failed"}`,
			wantErrText: "partially successful: some failed",
		},
		{
			name:        "UnexpectedStatusCode",
			getStatus:   http.StatusInternalServerError,
			payload:     `{}`,
			wantErrText: "500",
		},
		{
			name:        "InvalidJsonBody",
			getStatus:   http.StatusOK,
			payload:     `not-json`,
			wantErrText: "invalid character",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockContext := mocks.NewMockContext(t.Context())
			respondPublishPost(mockContext, http.StatusAccepted, `"d1"`, nil)
			respondDeploymentGet(mockContext, tc.getStatus, tc.payload)

			client, err := NewFuncAppHostClient(
				"host.example", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
			require.NoError(t, err)

			zip := bytes.NewReader([]byte{})
			resp, err := client.Publish(*mockContext.Context, zip, nil)
			if tc.wantOk {
				require.NoError(t, err)
				require.Equal(t, PublishStatusSuccess, resp.Status)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErrText)
			}
		})
	}
}

// TestFuncAppHostClient_WaitForDeployment_NotFoundFallback tests the specific
// branch where we receive a 404 after observing an in-progress response; the
// client treats that as "deployment is complete" and returns the last response.
func TestFuncAppHostClient_WaitForDeployment_NotFoundFallback(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	respondPublishPost(mockContext, http.StatusAccepted, `"d1"`, nil)

	var calls atomic.Int32
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/api/deployments/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		n := calls.Add(1)
		// First response: in-progress with a short Retry-After so the
		// production sleep between polls is effectively zero. Subsequent
		// responses return 404 which, combined with a recorded lastResponse,
		// triggers the "completed but 404" fallback path.
		if n == 1 {
			resp, err := mocks.CreateEmptyHttpResponse(request, http.StatusOK)
			if err != nil {
				return nil, err
			}
			body := `{"id":"d1","status":1,"status_text":"building","site_name":"app"}`
			resp.Body = makeBody(body)
			resp.ContentLength = int64(len(body))
			resp.Header.Set("Retry-After", "0")
			return resp, nil
		}
		resp, err := mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		if err != nil {
			return nil, err
		}
		resp.Body = http.NoBody
		return resp, nil
	})

	client, err := NewFuncAppHostClient("host.example", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
	require.NoError(t, err)

	zip := bytes.NewReader([]byte{})
	resp, err := client.Publish(*mockContext.Context, zip, nil)
	require.NoError(t, err)
	require.Equal(t, "d1", resp.Id)
	require.Equal(t, PublishStatusBuilding, resp.Status)
	require.Equal(t, "app", resp.SiteName)
}

// TestFuncAppHostClient_WaitForDeployment_ContextCanceled exercises the
// cancellation path inside the polling loop. We cancel the context while the
// server keeps returning "in-progress" responses.
func TestFuncAppHostClient_WaitForDeployment_ContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	mockContext := mocks.NewMockContext(ctx)
	respondPublishPost(mockContext, http.StatusAccepted, `"d1"`, nil)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/api/deployments/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		resp, err := mocks.CreateEmptyHttpResponse(request, http.StatusOK)
		if err != nil {
			return nil, err
		}
		body := `{"id":"d1","status":1}`
		resp.Body = makeBody(body)
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Retry-After", "0")
		// Cancel after first poll so the loop exits via ctx.Done().
		cancel()
		return resp, nil
	})

	client, err := NewFuncAppHostClient("host.example", &mocks.MockCredentials{}, mockContext.ArmClientOptions)
	require.NoError(t, err)

	zip := bytes.NewReader([]byte{})
	_, err = client.Publish(ctx, zip, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

// compile-time assertion kept for future extension hooks.
var _ = http.MethodPost
