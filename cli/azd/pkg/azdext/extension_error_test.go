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
	"google.golang.org/genproto/googleapis/rpc/errdetails"
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
			name: "GrpcUnauthenticatedError",
			inputErr: mustAuthStatusError(
				codes.Unauthenticated,
				AuthErrorReasonNotLoggedIn,
				"not logged in, run `azd auth login` to login",
			),
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Contains(t, protoErr.GetMessage(), "not logged in")

				localDetail := protoErr.GetLocalError()
				require.NotNil(t, localDetail)
				assert.Equal(t, "not_logged_in", localDetail.GetCode())
				assert.Equal(t, "auth", localDetail.GetCategory())

				var localErr *LocalError
				require.ErrorAs(t, goErr, &localErr)
				assert.Equal(t, LocalErrorCategoryAuth, localErr.Category)
				assert.Equal(t, "not_logged_in", localErr.Code)
			},
		},
		{
			name: "WrappedGrpcUnauthenticatedError",
			inputErr: fmt.Errorf(
				"failed to prompt: %w",
				mustAuthStatusError(
					codes.Unauthenticated,
					AuthErrorReasonTokenProtectionBlocked,
					"AADSTS530084: blocked by token protection",
				),
			),
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Equal(t, "AADSTS530084: blocked by token protection", protoErr.GetMessage())

				localDetail := protoErr.GetLocalError()
				require.NotNil(t, localDetail)
				assert.Equal(t, "token_protection_blocked", localDetail.GetCode())
				assert.Equal(t, "auth", localDetail.GetCategory())
			},
		},
		{
			name: "GrpcUnauthenticatedLoginRequiredError",
			inputErr: mustAuthStatusError(
				codes.Unauthenticated,
				AuthErrorReasonLoginRequired,
				"AADSTS70043: token expired\nlogin expired, run `azd auth login`",
			),
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Contains(t, protoErr.GetMessage(), "login expired")

				localDetail := protoErr.GetLocalError()
				require.NotNil(t, localDetail)
				assert.Equal(t, "login_required", localDetail.GetCode())
				assert.Equal(t, "auth", localDetail.GetCategory())

				var localErr *LocalError
				require.ErrorAs(t, goErr, &localErr)
				assert.Equal(t, LocalErrorCategoryAuth, localErr.Category)
				assert.Equal(t, "login_required", localErr.Code)
			},
		},
		{
			name:     "GrpcUnauthenticatedWithoutAuthDetailsFallsBackToAuthFailed",
			inputErr: status.Error(codes.Unauthenticated, "generic auth problem"),
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Equal(t, "generic auth problem", protoErr.GetMessage())

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

func mustAuthStatusError(code codes.Code, reason, message string) error {
	st := status.New(code, message)
	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: reason,
		Domain: AuthErrorDomain,
	})
	if err != nil {
		panic(err)
	}

	return withDetails.Err()
}
