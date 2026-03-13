// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExtensionError_RoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		inputErr error
		wantNil  bool
		verify   func(t *testing.T, protoErr *ExtensionError, goErr error)
	}{
		{
			name:     "NilError",
			inputErr: nil,
			wantNil:  true,
		},
		{
			name:     "SimpleError",
			inputErr: errors.New("simple error"),
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, "simple error", protoErr.GetMessage())
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_UNSPECIFIED, protoErr.GetOrigin())
				assert.Nil(t, protoErr.GetSource())

				assert.Equal(t, "simple error", goErr.Error())

				// Untyped errors round-trip as LocalError so the message is preserved
				// through the display and telemetry pipelines.
				var localErr *LocalError
				require.ErrorAs(t, goErr, &localErr)
				assert.Equal(t, "simple error", localErr.Message)
				assert.Equal(t, LocalErrorCategoryLocal, localErr.Category)
			},
		},
		{
			name: "ExtServiceError",
			inputErr: &ServiceError{
				Message:     "Rate limit exceeded",
				ErrorCode:   "RateLimitExceeded",
				StatusCode:  429,
				ServiceName: "openai.azure.com",
				Suggestion:  "Retry with exponential backoff",
			},
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, "Rate limit exceeded", protoErr.GetMessage())
				assert.Equal(t, "Retry with exponential backoff", protoErr.GetSuggestion())
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_SERVICE, protoErr.GetOrigin())

				svcDetail := protoErr.GetServiceError()
				require.NotNil(t, svcDetail)
				assert.Equal(t, "RateLimitExceeded", svcDetail.GetErrorCode())
				assert.Equal(t, int32(429), svcDetail.GetStatusCode())
				assert.Equal(t, "openai.azure.com", svcDetail.GetServiceName())

				var svcErr *ServiceError
				require.ErrorAs(t, goErr, &svcErr)
				assert.Equal(t, "Rate limit exceeded", svcErr.Message)
				assert.Equal(t, "RateLimitExceeded", svcErr.ErrorCode)
				assert.Equal(t, 429, svcErr.StatusCode)
				assert.Equal(t, "openai.azure.com", svcErr.ServiceName)
				assert.Equal(t, "Retry with exponential backoff", svcErr.Suggestion)
			},
		},
		{
			name: "ExtLocalError",
			inputErr: &LocalError{
				Message:    "invalid config",
				Code:       "invalid_config",
				Category:   LocalErrorCategoryValidation,
				Suggestion: "Add the missing required field",
			},
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Equal(t, "Add the missing required field", protoErr.GetSuggestion())

				localDetail := protoErr.GetLocalError()
				require.NotNil(t, localDetail)
				assert.Equal(t, "invalid_config", localDetail.GetCode())
				assert.Equal(t, "validation", localDetail.GetCategory())

				var localErr *LocalError
				require.ErrorAs(t, goErr, &localErr)
				assert.Equal(t, "invalid_config", localErr.Code)
				assert.Equal(t, LocalErrorCategoryValidation, localErr.Category)
				assert.Equal(t, "Add the missing required field", localErr.Suggestion)
			},
		},
		{
			name: "AzCoreResponseError",
			inputErr: &azcore.ResponseError{
				ErrorCode:  "ResourceNotFound",
				StatusCode: 404,
			},
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_SERVICE, protoErr.GetOrigin())

				svcDetail := protoErr.GetServiceError()
				require.NotNil(t, svcDetail)
				assert.Equal(t, "ResourceNotFound", svcDetail.GetErrorCode())
				assert.Equal(t, int32(404), svcDetail.GetStatusCode())

				var svcErr *ServiceError
				require.ErrorAs(t, goErr, &svcErr)
				assert.Equal(t, "ResourceNotFound", svcErr.ErrorCode)
				assert.Equal(t, 404, svcErr.StatusCode)
			},
		},
		{
			name:     "GrpcUnauthenticatedError",
			inputErr: status.Error(codes.Unauthenticated, "not logged in, run `azd auth login` to login"),
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Contains(t, protoErr.GetMessage(), "not logged in")

				localDetail := protoErr.GetLocalError()
				require.NotNil(t, localDetail)
				assert.Equal(t, "auth_failed", localDetail.GetCode())
				assert.Equal(t, "auth", localDetail.GetCategory())

				var localErr *LocalError
				require.ErrorAs(t, goErr, &localErr)
				assert.Equal(t, LocalErrorCategoryAuth, localErr.Category)
				assert.Equal(t, "auth_failed", localErr.Code)
			},
		},
		{
			name:     "WrappedGrpcUnauthenticatedError",
			inputErr: fmt.Errorf("failed to prompt: %w", status.Error(codes.Unauthenticated, "login expired")),
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Equal(t, "login expired", protoErr.GetMessage())

				localDetail := protoErr.GetLocalError()
				require.NotNil(t, localDetail)
				assert.Equal(t, "auth_failed", localDetail.GetCode())
				assert.Equal(t, "auth", localDetail.GetCategory())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protoErr := WrapError(tt.inputErr)

			if tt.wantNil {
				assert.Nil(t, protoErr)
				assert.Nil(t, UnwrapError(nil))
				return
			}

			require.NotNil(t, protoErr)
			goErr := UnwrapError(protoErr)
			require.NotNil(t, goErr)

			tt.verify(t, protoErr, goErr)
		})
	}
}

func TestLocalError_Unwrap(t *testing.T) {
	cause := errors.New("root cause")
	localErr := &LocalError{
		Message:  "validation failed",
		Code:     "bad_input",
		Category: LocalErrorCategoryValidation,
		Cause:    cause,
	}

	// errors.Is traverses through LocalError to cause
	assert.True(t, errors.Is(localErr, cause))

	// errors.Unwrap returns the cause
	assert.Equal(t, cause, errors.Unwrap(localErr))

	// Nil cause returns nil from Unwrap
	noCause := &LocalError{Message: "no cause"}
	assert.Nil(t, errors.Unwrap(noCause))
}

func TestServiceError_Unwrap(t *testing.T) {
	cause := errors.New("connection timeout")
	svcErr := &ServiceError{
		Message:     "request failed",
		ErrorCode:   "Timeout",
		StatusCode:  504,
		ServiceName: "api.example.com",
		Cause:       cause,
	}

	// errors.Is traverses through ServiceError to cause
	assert.True(t, errors.Is(svcErr, cause))

	// errors.Unwrap returns the cause
	assert.Equal(t, cause, errors.Unwrap(svcErr))

	// Nil cause returns nil from Unwrap
	noCause := &ServiceError{Message: "no cause"}
	assert.Nil(t, errors.Unwrap(noCause))
}

func TestWrapError_NestedStructuredErrors(t *testing.T) {
	// Scenario: inner LocalError wrapped by outer ServiceError (via Cause).
	// WrapError should pick the outermost (ServiceError).
	innerLocal := &LocalError{
		Message:  "config missing",
		Code:     "missing_config",
		Category: LocalErrorCategoryValidation,
	}
	outerService := &ServiceError{
		Message:     "deployment failed",
		ErrorCode:   "DeployFailed",
		StatusCode:  500,
		ServiceName: "mgmt.azure.com",
		Cause:       innerLocal,
	}

	protoErr := WrapError(outerService)
	require.NotNil(t, protoErr)
	assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_SERVICE, protoErr.GetOrigin())
	assert.Equal(t, "deployment failed", protoErr.GetMessage())

	svcDetail := protoErr.GetServiceError()
	require.NotNil(t, svcDetail)
	assert.Equal(t, "DeployFailed", svcDetail.GetErrorCode())
}

func TestWrapError_FmtWrappedStructuredError(t *testing.T) {
	// Scenario: structured error wrapped by fmt.Errorf (common pattern).
	// WrapError should still detect the structured error via errors.As.
	local := &LocalError{
		Message:    "invalid manifest",
		Code:       "invalid_manifest",
		Category:   LocalErrorCategoryValidation,
		Suggestion: "check your agent.yaml",
	}
	wrapped := fmt.Errorf("init failed: %w", local)

	protoErr := WrapError(wrapped)
	require.NotNil(t, protoErr)
	assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
	assert.Equal(t, "invalid manifest", protoErr.GetMessage())
	assert.Equal(t, "check your agent.yaml", protoErr.GetSuggestion())

	localDetail := protoErr.GetLocalError()
	require.NotNil(t, localDetail)
	assert.Equal(t, "invalid_manifest", localDetail.GetCode())
	assert.Equal(t, "validation", localDetail.GetCategory())
}
