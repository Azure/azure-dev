// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"
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
				Links: []errorhandler.ErrorLink{{
					URL:   "https://aka.ms/azd-errors#rate-limit",
					Title: "Rate limit troubleshooting",
				}},
			},
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, "Rate limit exceeded", protoErr.GetMessage())
				assert.Equal(t, "Retry with exponential backoff", protoErr.GetSuggestion())
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_SERVICE, protoErr.GetOrigin())
				require.Len(t, protoErr.GetLinks(), 1)
				assert.Equal(t, "https://aka.ms/azd-errors#rate-limit", protoErr.GetLinks()[0].GetUrl())
				assert.Equal(t, "Rate limit troubleshooting", protoErr.GetLinks()[0].GetTitle())

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
				require.Len(t, svcErr.Links, 1)
				assert.Equal(t, "https://aka.ms/azd-errors#rate-limit", svcErr.Links[0].URL)
				assert.Equal(t, "Rate limit troubleshooting", svcErr.Links[0].Title)
			},
		},
		{
			name: "ExtLocalError",
			inputErr: &LocalError{
				Message:    "invalid config",
				Code:       "invalid_config",
				Category:   LocalErrorCategoryValidation,
				Suggestion: "Add the missing required field",
				Links: []errorhandler.ErrorLink{{
					URL:   "https://aka.ms/azd-errors#invalid-config",
					Title: "Invalid config reference",
				}},
			},
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Equal(t, "Add the missing required field", protoErr.GetSuggestion())
				require.Len(t, protoErr.GetLinks(), 1)
				assert.Equal(t, "https://aka.ms/azd-errors#invalid-config", protoErr.GetLinks()[0].GetUrl())

				localDetail := protoErr.GetLocalError()
				require.NotNil(t, localDetail)
				assert.Equal(t, "invalid_config", localDetail.GetCode())
				assert.Equal(t, "validation", localDetail.GetCategory())

				var localErr *LocalError
				require.ErrorAs(t, goErr, &localErr)
				assert.Equal(t, "invalid_config", localErr.Code)
				assert.Equal(t, LocalErrorCategoryValidation, localErr.Category)
				assert.Equal(t, "Add the missing required field", localErr.Suggestion)
				require.Len(t, localErr.Links, 1)
				assert.Equal(t, "Invalid config reference", localErr.Links[0].Title)
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
					"AADSTS530084",
					"AADSTS530084: blocked by token protection",
				),
			),
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Equal(t, "AADSTS530084: blocked by token protection", protoErr.GetMessage())

				localDetail := protoErr.GetLocalError()
				require.NotNil(t, localDetail)
				// AAD-originated reasons collapse to the generic "auth_failed" code; the raw
				// "AADSTS530084" reason remains available to extensions via the gRPC ErrorInfo.
				assert.Equal(t, "auth_failed", localDetail.GetCode())
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
			name: "GrpcUnauthenticatedWithActionableDetail",
			inputErr: mustStatusErrorWithDetails(
				codes.Unauthenticated,
				"A Conditional Access token protection policy blocked this token request.",
				&errdetails.ErrorInfo{
					Reason: "AADSTS530084",
					Domain: AuthErrorDomain,
				},
				&ActionableErrorDetail{
					Suggestion: "Contact your IT administrator or request a policy exception.",
					Links: []*ErrorLink{{
						Url:   "https://aka.ms/TokenProtectionFAQ#troubleshooting",
						Title: "Token protection FAQ",
					}},
				},
			),
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Equal(t,
					"A Conditional Access token protection policy blocked this token request.",
					protoErr.GetMessage())
				assert.Equal(t, "Contact your IT administrator or request a policy exception.", protoErr.GetSuggestion())
				require.Len(t, protoErr.GetLinks(), 1)
				assert.Equal(t, "https://aka.ms/TokenProtectionFAQ#troubleshooting", protoErr.GetLinks()[0].GetUrl())

				localDetail := protoErr.GetLocalError()
				require.NotNil(t, localDetail)
				assert.Equal(t, "auth_failed", localDetail.GetCode())
				assert.Equal(t, "auth", localDetail.GetCategory())

				var localErr *LocalError
				require.ErrorAs(t, goErr, &localErr)
				assert.Equal(t, LocalErrorCategoryAuth, localErr.Category)
				assert.Equal(t, "auth_failed", localErr.Code)
				assert.Equal(t, "Contact your IT administrator or request a policy exception.", localErr.Suggestion)
				require.Len(t, localErr.Links, 1)
			},
		},
		{
			name: "GrpcActionableNonAuthError",
			inputErr: mustStatusErrorWithDetails(
				codes.InvalidArgument,
				"The extension configuration is invalid.",
				&ActionableErrorDetail{
					Suggestion: "Fix the extension config and retry.",
					Links: []*ErrorLink{{
						Url:   "https://aka.ms/azd-errors#invalid-config",
						Title: "Invalid config reference",
					}},
				},
			),
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Equal(t, "The extension configuration is invalid.", protoErr.GetMessage())
				assert.Equal(t, "Fix the extension config and retry.", protoErr.GetSuggestion())
				require.Len(t, protoErr.GetLinks(), 1)

				localDetail := protoErr.GetLocalError()
				require.NotNil(t, localDetail)
				assert.Equal(t, string(LocalErrorCategoryLocal), localDetail.GetCategory())

				var localErr *LocalError
				require.ErrorAs(t, goErr, &localErr)
				assert.Equal(t, LocalErrorCategoryLocal, localErr.Category)
				assert.Equal(t, "Fix the extension config and retry.", localErr.Suggestion)
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

func TestUnwrapError_EmptyMessagePreservesStructuredError(t *testing.T) {
	protoErr := &ExtensionError{
		Origin: ErrorOrigin_ERROR_ORIGIN_LOCAL,
		Source: &ExtensionError_LocalError{
			LocalError: &LocalErrorDetail{
				Code:     "empty_message",
				Category: "validation",
			},
		},
		Suggestion: "Fill in the required setting",
		Links: []*ErrorLink{{
			Url:   "https://aka.ms/azd-errors#empty-message",
			Title: "Validation troubleshooting",
		}},
	}

	err := UnwrapError(protoErr)
	require.Error(t, err)

	localErr, ok := errors.AsType[*LocalError](err)
	require.True(t, ok)
	assert.Empty(t, localErr.Message)
	assert.Equal(t, "empty_message", localErr.Code)
	assert.Equal(t, LocalErrorCategoryValidation, localErr.Category)
	assert.Equal(t, "Fill in the required setting", localErr.Suggestion)
	require.Len(t, localErr.Links, 1)
	assert.Equal(t, "Validation troubleshooting", localErr.Links[0].Title)
}

func mustAuthStatusError(code codes.Code, reason, message string) error {
	return mustStatusErrorWithDetails(code, message, &errdetails.ErrorInfo{
		Reason: reason,
		Domain: AuthErrorDomain,
	})
}

func mustStatusErrorWithDetails(code codes.Code, message string, details ...protoadapt.MessageV1) error {
	st := status.New(code, message)
	withDetails, err := st.WithDetails(details...)
	if err != nil {
		panic(err)
	}

	return withDetails.Err()
}

func TestGRPCStatusFromError(t *testing.T) {
	t.Parallel()

	t.Run("nil error returns false", func(t *testing.T) {
		st, ok := GRPCStatusFromError(nil)
		assert.False(t, ok)
		assert.Nil(t, st)
	})

	t.Run("non-gRPC error returns false", func(t *testing.T) {
		st, ok := GRPCStatusFromError(errors.New("plain error"))
		assert.False(t, ok)
		assert.Nil(t, st)
	})

	t.Run("status error returns status", func(t *testing.T) {
		original := status.New(codes.NotFound, "missing").Err()
		st, ok := GRPCStatusFromError(original)
		require.True(t, ok)
		require.NotNil(t, st)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Equal(t, "missing", st.Message())
	})

	t.Run("status error wrapped with fmt.Errorf is unwrapped", func(t *testing.T) {
		original := status.New(codes.NotFound, "missing").Err()
		wrapped := fmt.Errorf("context: %w", original)
		st, ok := GRPCStatusFromError(wrapped)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

func TestActionableErrorDetailFromStatus(t *testing.T) {
	t.Parallel()

	t.Run("nil status returns nil", func(t *testing.T) {
		assert.Nil(t, ActionableErrorDetailFromStatus(nil))
	})

	t.Run("status without ActionableErrorDetail returns nil", func(t *testing.T) {
		st := status.New(codes.Unknown, "no details")
		assert.Nil(t, ActionableErrorDetailFromStatus(st))
	})

	t.Run("status with ActionableErrorDetail returns it", func(t *testing.T) {
		err := mustStatusErrorWithDetails(codes.Unknown, "boom", &ActionableErrorDetail{
			Suggestion: "try harder",
		})
		st, _ := status.FromError(err)
		actionable := ActionableErrorDetailFromStatus(st)
		require.NotNil(t, actionable)
		assert.Equal(t, "try harder", actionable.GetSuggestion())
	})
}

func TestActionableErrorDetailFromError(t *testing.T) {
	t.Parallel()

	t.Run("nil error returns nil", func(t *testing.T) {
		assert.Nil(t, ActionableErrorDetailFromError(nil))
	})

	t.Run("non-gRPC error returns nil", func(t *testing.T) {
		assert.Nil(t, ActionableErrorDetailFromError(errors.New("plain")))
	})

	t.Run("status error without detail returns nil", func(t *testing.T) {
		assert.Nil(t, ActionableErrorDetailFromError(status.New(codes.Unknown, "no details").Err()))
	})

	t.Run("status error with detail returns it (even when wrapped)", func(t *testing.T) {
		statusErr := mustStatusErrorWithDetails(codes.Unknown, "boom", &ActionableErrorDetail{
			Suggestion: "try harder",
		})
		wrapped := fmt.Errorf("context: %w", statusErr)
		actionable := ActionableErrorDetailFromError(wrapped)
		require.NotNil(t, actionable)
		assert.Equal(t, "try harder", actionable.GetSuggestion())
	})
}
