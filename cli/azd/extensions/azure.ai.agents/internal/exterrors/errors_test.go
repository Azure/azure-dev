// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestServiceFromAzure(t *testing.T) {
	responseErr := &azcore.ResponseError{
		StatusCode: http.StatusTooManyRequests,
		ErrorCode:  "TooManyRequests",
		RawResponse: &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"TooManyRequests"}}`)),
			Request: &http.Request{
				Method: http.MethodPost,
				URL: &url.URL{
					Scheme: "https",
					Host:   "sample.services.ai.azure.com",
				},
			},
		},
	}

	result := ServiceFromAzure(responseErr, OpCreateAgent)

	var serviceErr *azdext.ServiceError
	require.ErrorAs(t, result, &serviceErr)
	assert.Equal(t, "create_agent.TooManyRequests", serviceErr.ErrorCode)
	assert.Equal(t, http.StatusTooManyRequests, serviceErr.StatusCode)
	assert.Equal(t, "sample.services.ai.azure.com", serviceErr.ServiceName)
}

func TestServiceFromAzurePreservesStructuredError(t *testing.T) {
	expected := &azdext.LocalError{
		Message:  "invalid input",
		Code:     "invalid_input",
		Category: azdext.LocalErrorCategoryValidation,
	}

	assert.Same(t, expected, ServiceFromAzure(expected, OpCreateAgent))
}

func TestFromHostPreservesServiceDetails(t *testing.T) {
	st := status.New(codes.Unknown, "request failed")
	withDetails, err := st.WithDetails(
		&azdext.ServiceErrorDetail{
			ErrorCode:   "AuthorizationFailed",
			StatusCode:  http.StatusForbidden,
			ServiceName: "management.azure.com",
		},
		&azdext.ActionableErrorDetail{
			Suggestion: "request the required role and retry",
		},
	)
	require.NoError(t, err)

	result := FromHost(withDetails.Err(), OpContainerPublish, "container publish failed")

	var serviceErr *azdext.ServiceError
	require.ErrorAs(t, result, &serviceErr)
	assert.Equal(t, "container_publish.AuthorizationFailed", serviceErr.ErrorCode)
	assert.Equal(t, http.StatusForbidden, serviceErr.StatusCode)
	assert.Equal(t, "management.azure.com", serviceErr.ServiceName)
	assert.Equal(t, "request the required role and retry", serviceErr.Suggestion)
	assert.Contains(t, serviceErr.Message, "container publish failed")
}

func TestFromHostPreservesRelayedServiceError(t *testing.T) {
	source := &azdext.ServiceError{
		Message:     "could not get Foundry project",
		ErrorCode:   "get_foundry_project.AuthorizationFailed",
		StatusCode:  http.StatusForbidden,
		ServiceName: "management.azure.com",
		Suggestion:  "request the required role",
		Links: []errorhandler.ErrorLink{{
			URL:   "https://aka.ms/foundry-project-access",
			Title: "Foundry project access",
		}},
	}
	st, err := status.New(codes.Unknown, "container publish failed").WithDetails(azdext.WrapError(source))
	require.NoError(t, err)

	result := FromHost(st.Err(), OpContainerPublish, "container publish failed")

	var serviceErr *azdext.ServiceError
	require.ErrorAs(t, result, &serviceErr)
	assert.Equal(t, source.Message, serviceErr.Message)
	assert.Equal(t, source.ErrorCode, serviceErr.ErrorCode)
	assert.Equal(t, source.StatusCode, serviceErr.StatusCode)
	assert.Equal(t, source.ServiceName, serviceErr.ServiceName)
	assert.Equal(t, source.Suggestion, serviceErr.Suggestion)
	assert.Equal(t, source.Links, serviceErr.Links)
}

func TestFromHostPreservesRelayedLocalError(t *testing.T) {
	source := &azdext.LocalError{
		Message:    "invalid Foundry project resource ID",
		Code:       "invalid_ai_project_id",
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: "verify AZURE_AI_PROJECT_ID",
		Links: []errorhandler.ErrorLink{{
			URL:   "https://aka.ms/azd-ai-project-id",
			Title: "Foundry project configuration",
		}},
	}
	st, err := status.New(codes.Unknown, "container publish failed").WithDetails(azdext.WrapError(source))
	require.NoError(t, err)

	result := FromHost(st.Err(), OpContainerPublish, "container publish failed")

	var localErr *azdext.LocalError
	require.ErrorAs(t, result, &localErr)
	assert.Equal(t, source.Message, localErr.Message)
	assert.Equal(t, source.Code, localErr.Code)
	assert.Equal(t, source.Category, localErr.Category)
	assert.Equal(t, source.Suggestion, localErr.Suggestion)
	assert.Equal(t, source.Links, localErr.Links)
}

func TestFromHost_RelayedErrorUsesOuterActionableGuidance(t *testing.T) {
	source := &azdext.ServiceError{
		Message:     "could not get Foundry project",
		ErrorCode:   "get_foundry_project.AuthorizationFailed",
		StatusCode:  http.StatusForbidden,
		ServiceName: "management.azure.com",
		Suggestion:  "request the required role",
		Links: []errorhandler.ErrorLink{{
			URL:   "https://aka.ms/foundry-project-access",
			Title: "Foundry project access",
		}},
	}
	st, err := status.New(codes.Unknown, "container publish failed").WithDetails(
		azdext.WrapError(source),
		&azdext.ActionableErrorDetail{
			Suggestion: "refresh the environment and retry",
			Links: []*azdext.ErrorLink{{
				Url:   "https://aka.ms/azd-env-refresh",
				Title: "Environment refresh",
			}},
		},
	)
	require.NoError(t, err)

	result := FromHost(st.Err(), OpContainerPublish, "container publish failed")

	var serviceErr *azdext.ServiceError
	require.ErrorAs(t, result, &serviceErr)
	assert.Equal(t, source.ErrorCode, serviceErr.ErrorCode)
	assert.Equal(t, "refresh the environment and retry", serviceErr.Suggestion)
	assert.Equal(t, []errorhandler.ErrorLink{{
		URL:   "https://aka.ms/azd-env-refresh",
		Title: "Environment refresh",
	}}, serviceErr.Links)
}

func TestFromHost_PrioritizesCancellationAndAuthentication(t *testing.T) {
	relayed := azdext.WrapError(&azdext.LocalError{
		Message:  "invalid project",
		Code:     "invalid_ai_project_id",
		Category: azdext.LocalErrorCategoryValidation,
	})

	t.Run("cancellation", func(t *testing.T) {
		st, err := status.New(codes.Canceled, "cancelled").WithDetails(relayed)
		require.NoError(t, err)

		result := FromHost(st.Err(), OpContainerPublish, "container publish failed")

		var localErr *azdext.LocalError
		require.ErrorAs(t, result, &localErr)
		assert.Equal(t, azdext.LocalErrorCategoryUser, localErr.Category)
		assert.Equal(t, CodeCancelled, localErr.Code)
	})

	t.Run("authentication", func(t *testing.T) {
		st, err := status.New(codes.Unauthenticated, "not logged in").WithDetails(relayed)
		require.NoError(t, err)

		result := FromHost(st.Err(), OpContainerPublish, "container publish failed")

		var localErr *azdext.LocalError
		require.ErrorAs(t, result, &localErr)
		assert.Equal(t, azdext.LocalErrorCategoryAuth, localErr.Category)
		assert.Equal(t, CodeNotLoggedIn, localErr.Code)
	})
}

func TestInternalFromErrorPreservesStructuredError(t *testing.T) {
	expected := &azdext.LocalError{
		Message:  "missing dependency",
		Code:     "missing_dependency",
		Category: azdext.LocalErrorCategoryDependency,
	}

	assert.Same(t, expected, InternalFromError(expected, OpContainerPackage, "code packaging failed"))
}

func TestFromAiService(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		fallbackCode string
		wantCategory azdext.LocalErrorCategory
		wantCode     string
	}{
		{
			name:         "Unauthenticated returns Auth with not_logged_in",
			err:          status.Error(codes.Unauthenticated, "not logged in, run `azd auth login` to login"),
			fallbackCode: "model_catalog_failed",
			wantCategory: azdext.LocalErrorCategoryAuth,
			wantCode:     CodeNotLoggedIn,
		},
		{
			name:         "Unauthenticated returns Auth with login_expired",
			err:          status.Error(codes.Unauthenticated, "AADSTS70043: token expired\nlogin expired, run `azd auth login`"),
			fallbackCode: "model_catalog_failed",
			wantCategory: azdext.LocalErrorCategoryAuth,
			wantCode:     CodeLoginExpired,
		},
		{
			name:         "Unauthenticated returns Auth with generic auth_failed",
			err:          status.Error(codes.Unauthenticated, "insufficient permissions for this operation"),
			fallbackCode: "model_catalog_failed",
			wantCategory: azdext.LocalErrorCategoryAuth,
			wantCode:     CodeAuthFailed,
		},
		{
			name:         "Other gRPC error returns Internal",
			err:          status.Error(codes.InvalidArgument, "missing subscription"),
			fallbackCode: "model_catalog_failed",
			wantCategory: azdext.LocalErrorCategoryInternal,
			wantCode:     "model_catalog_failed",
		},
		{
			name:         "Canceled returns User cancellation",
			err:          status.Error(codes.Canceled, "cancelled"),
			fallbackCode: "model_catalog_failed",
			wantCategory: azdext.LocalErrorCategoryUser,
			wantCode:     CodeCancelled,
		},
		{
			name:         "Nil returns nil",
			err:          nil,
			fallbackCode: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FromAiService(tt.err, tt.fallbackCode)
			if tt.err == nil {
				assert.Nil(t, result)
				return
			}

			var localErr *azdext.LocalError
			require.ErrorAs(t, result, &localErr)
			assert.Equal(t, tt.wantCategory, localErr.Category)
			assert.Equal(t, tt.wantCode, localErr.Code)
		})
	}
}

func TestFromPrompt(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		contextMsg   string
		wantCategory azdext.LocalErrorCategory
		wantCode     string
		wantContain  string
	}{
		{
			name:         "Auth error returns structured Auth error with context",
			err:          status.Error(codes.Unauthenticated, "not logged in, run `azd auth login` to login"),
			contextMsg:   "failed to prompt for subscription",
			wantCategory: azdext.LocalErrorCategoryAuth,
			wantCode:     CodeNotLoggedIn,
			wantContain:  "failed to prompt for subscription",
		},
		{
			name:         "Login expired returns structured Auth error with context",
			err:          status.Error(codes.Unauthenticated, "AADSTS70043: token expired"),
			contextMsg:   "failed to prompt for location",
			wantCategory: azdext.LocalErrorCategoryAuth,
			wantCode:     CodeLoginExpired,
			wantContain:  "failed to prompt for location",
		},
		{
			name:         "Cancellation returns User error",
			err:          context.Canceled,
			contextMsg:   "subscription selection was cancelled",
			wantCategory: azdext.LocalErrorCategoryUser,
			wantCode:     CodeCancelled,
		},
		{
			name:        "Non-auth error returns wrapped error",
			err:         status.Error(codes.Internal, "server error"),
			contextMsg:  "failed to prompt for subscription",
			wantContain: "failed to prompt for subscription",
		},
		{
			name: "Nil returns nil",
			err:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FromPrompt(tt.err, tt.contextMsg)
			if tt.err == nil {
				assert.Nil(t, result)
				return
			}

			if tt.wantCategory != "" {
				var localErr *azdext.LocalError
				require.ErrorAs(t, result, &localErr)
				assert.Equal(t, tt.wantCategory, localErr.Category)
				assert.Equal(t, tt.wantCode, localErr.Code)
			}
			if tt.wantContain != "" {
				assert.Contains(t, result.Error(), tt.wantContain)
			}
		})
	}
}

func TestIsPromptRequired(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "gRPC status with prompt required message",
			err:  status.Error(codes.Unknown, "prompt required"),
			want: true,
		},
		{
			name: "gRPC status with prompt required mixed case",
			err:  status.Error(codes.Unknown, "Prompt Required"),
			want: true,
		},
		{
			name: "plain error wrapping prompt required text",
			err:  errors.New("rpc error: prompt required: Select subscription"),
			want: true,
		},
		{
			name: "unrelated transport error",
			err:  status.Error(codes.Unavailable, "connection refused"),
			want: false,
		},
		{
			name: "auth error",
			err:  status.Error(codes.Unauthenticated, "not logged in"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsPromptRequired(tt.err))
		})
	}
}
