// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/errorchain"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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
				CauseTypes: []string{"*agents.TransportError", "*agents.ConfigError"},
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
				assert.Equal(t, []string{"*agents.TransportError", "*agents.ConfigError"}, localDetail.GetCauseTypes())

				var localErr *LocalError
				require.ErrorAs(t, goErr, &localErr)
				assert.Equal(t, "invalid_config", localErr.Code)
				assert.Equal(t, LocalErrorCategoryValidation, localErr.Category)
				assert.Equal(t, []string{"*agents.TransportError", "*agents.ConfigError"}, localErr.CauseTypes)
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

func TestExtensionError_ToolErrorRoundTrip(t *testing.T) {
	t.Parallel()

	exitCode := 23
	source := &ToolError{
		Message:    "docker build failed",
		ToolName:   "docker",
		Kind:       ToolErrorKindFailed,
		ExitCode:   &exitCode,
		Suggestion: "Check the Docker build output",
	}

	protoErr := WrapError(fmt.Errorf("container build: %w", source))
	require.NotNil(t, protoErr)
	assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_TOOL, protoErr.GetOrigin())
	assert.Equal(t, "docker build failed", protoErr.GetMessage())
	require.NotNil(t, protoErr.GetToolError())
	assert.Equal(t, "docker", protoErr.GetToolError().GetToolName())
	assert.Equal(t, "failed", protoErr.GetToolError().GetFailureKind())
	require.NotNil(t, protoErr.GetToolError().ExitCode)
	assert.Equal(t, int64(23), protoErr.GetToolError().GetExitCode())

	unwrapped := UnwrapError(protoErr)
	var toolErr *ToolError
	require.ErrorAs(t, unwrapped, &toolErr)
	assert.Equal(t, "docker build failed", toolErr.Message)
	assert.Equal(t, "docker", toolErr.ToolName)
	assert.Equal(t, ToolErrorKindFailed, toolErr.Kind)
	require.NotNil(t, toolErr.ExitCode)
	assert.Equal(t, 23, *toolErr.ExitCode)
	assert.Equal(t, "Check the Docker build output", toolErr.Suggestion)
}

func TestExtensionError_ToolErrorExitCodeWireRoundTrip(t *testing.T) {
	t.Parallel()

	const windowsExitCode int64 = 0xC0000135
	input := &ExtensionError{
		Message: "tool failed",
		Origin:  ErrorOrigin_ERROR_ORIGIN_TOOL,
		Source: &ExtensionError_ToolError{
			ToolError: &ToolErrorDetail{
				ToolName:    "tool",
				FailureKind: "failed",
				ExitCode:    new(windowsExitCode),
			},
		},
	}

	encoded, err := proto.Marshal(input)
	require.NoError(t, err)

	var decoded ExtensionError
	require.NoError(t, proto.Unmarshal(encoded, &decoded))
	require.Equal(t, windowsExitCode, decoded.GetToolError().GetExitCode())
}

func TestErrorDetailsFromStatus(t *testing.T) {
	t.Parallel()

	extensionErr := WrapError(&ToolError{
		Message:  "docker build failed",
		ToolName: "docker",
		Kind:     ToolErrorKindFailed,
	})
	st, err := status.New(codes.Unknown, "container build failed").WithDetails(extensionErr)
	require.NoError(t, err)

	assert.Equal(t, extensionErr.GetMessage(), ExtensionErrorFromStatus(st).GetMessage())
	assert.Nil(t, ServiceErrorDetailFromStatus(st))

	serviceDetail := &ServiceErrorDetail{
		ErrorCode:   "InternalServerError",
		StatusCode:  500,
		ServiceName: "management.azure.com",
	}
	st, err = status.New(codes.Unknown, "service failed").WithDetails(serviceDetail)
	require.NoError(t, err)

	assert.Equal(t, serviceDetail.GetErrorCode(), ServiceErrorDetailFromStatus(st).GetErrorCode())
	assert.Nil(t, ExtensionErrorFromStatus(st))
	assert.Nil(t, ExtensionErrorFromStatus(nil))
	assert.Nil(t, ServiceErrorDetailFromStatus(nil))
}

func TestWrapError_RelaysStructuredStatusDetails(t *testing.T) {
	t.Parallel()

	serviceErr := &ServiceError{
		Message:     "service request failed",
		ErrorCode:   "AuthorizationFailed",
		StatusCode:  403,
		ServiceName: "management.azure.com",
		Suggestion:  "request the required role",
		Links: []errorhandler.ErrorLink{{
			URL:   "https://aka.ms/azd-errors#authorization",
			Title: "Authorization help",
		}},
	}
	serviceDetail := WrapError(serviceErr)
	serviceStatus := mustStatusErrorWithDetails(
		codes.Unknown,
		"host service request failed",
		serviceDetail,
	)

	serviceWrapped := WrapError(serviceStatus)
	require.Equal(t, "host service request failed", serviceWrapped.GetMessage())
	require.Equal(t, ErrorOrigin_ERROR_ORIGIN_SERVICE, serviceWrapped.GetOrigin())
	require.Equal(t, serviceErr.Suggestion, serviceWrapped.GetSuggestion())
	require.Len(t, serviceWrapped.GetLinks(), 1)
	require.Equal(t, serviceDetail.GetLinks()[0].GetUrl(),
		serviceWrapped.GetLinks()[0].GetUrl())
	require.Equal(t, serviceDetail.GetLinks()[0].GetTitle(),
		serviceWrapped.GetLinks()[0].GetTitle())
	require.Equal(t, serviceErr.ErrorCode, serviceWrapped.GetServiceError().GetErrorCode())
	require.Equal(t, serviceDetail.GetServiceError().GetStatusCode(),
		serviceWrapped.GetServiceError().GetStatusCode())

	toolDetail := WrapError(&ToolError{
		Message:  "docker build failed",
		ToolName: "docker",
		Kind:     ToolErrorKindFailed,
	})
	toolStatus := mustStatusErrorWithDetails(
		codes.Unknown,
		"host docker build failed",
		toolDetail,
	)

	toolWrapped := WrapError(toolStatus)
	require.Equal(t, "host docker build failed", toolWrapped.GetMessage())
	require.Equal(t, ErrorOrigin_ERROR_ORIGIN_TOOL, toolWrapped.GetOrigin())
	require.Equal(t, "docker", toolWrapped.GetToolError().GetToolName())
	require.Equal(t, "failed", toolWrapped.GetToolError().GetFailureKind())
}

func TestWrapError_ConvertsServiceDetailFromStatus(t *testing.T) {
	t.Parallel()

	serviceDetail := &ServiceErrorDetail{
		ErrorCode:   "TooManyRequests",
		StatusCode:  429,
		ServiceName: "management.azure.com",
	}
	err := mustStatusErrorWithDetails(
		codes.Unknown,
		"host service request failed",
		serviceDetail,
	)

	wrapped := WrapError(err)
	require.Equal(t, "host service request failed", wrapped.GetMessage())
	require.Equal(t, ErrorOrigin_ERROR_ORIGIN_SERVICE, wrapped.GetOrigin())
	require.Equal(t, serviceDetail.GetErrorCode(), wrapped.GetServiceError().GetErrorCode())
	require.Equal(t, serviceDetail.GetStatusCode(), wrapped.GetServiceError().GetStatusCode())
	require.Equal(t, serviceDetail.GetServiceName(),
		wrapped.GetServiceError().GetServiceName())
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
	assert.Nil(t, localErr.CauseTypes)
	assert.Equal(t, "Fill in the required setting", localErr.Suggestion)
	require.Len(t, localErr.Links, 1)
	assert.Equal(t, "Validation troubleshooting", localErr.Links[0].Title)
}

func TestExtensionError_CauseTypesCopiedAtBoundaries(t *testing.T) {
	t.Parallel()

	sourceTypes := []string{
		"*agents.TransportError",
		"*fmt.wrapError",
		"*agents.TransportError",
		"invalid type",
	}
	for i := range errorchain.MaxChainLen {
		sourceTypes = append(sourceTypes, fmt.Sprintf("*agents.Cause%02d", i))
	}
	expectedTypes := []string{"*agents.TransportError"}
	for i := range errorchain.MaxChainLen - 1 {
		expectedTypes = append(expectedTypes, fmt.Sprintf("*agents.Cause%02d", i))
	}
	source := &LocalError{
		Message:    "unexpected failure",
		Code:       "unexpected_failure",
		Category:   LocalErrorCategoryInternal,
		CauseTypes: sourceTypes,
	}

	protoErr := WrapError(source)
	require.Equal(t, expectedTypes, protoErr.GetLocalError().GetCauseTypes())
	sourceTypes[0] = "*agents.MutatedSourceError"
	require.Equal(t, "*agents.TransportError", protoErr.GetLocalError().GetCauseTypes()[0])

	unwrapped := UnwrapError(protoErr)
	protoErr.GetLocalError().CauseTypes[1] = "*agents.MutatedProtoError"

	var localErr *LocalError
	require.ErrorAs(t, unwrapped, &localErr)
	require.Equal(t, expectedTypes, localErr.CauseTypes)
	localErr.CauseTypes[0] = "*agents.MutatedUnwrappedError"
	require.Equal(t, "*agents.TransportError", protoErr.GetLocalError().GetCauseTypes()[0])
}

func TestUnwrapError_NormalizesCauseTypes(t *testing.T) {
	t.Parallel()

	protoErr := &ExtensionError{
		Source: &ExtensionError_LocalError{
			LocalError: &LocalErrorDetail{
				CauseTypes: []string{
					"*agents.TransportError",
					"*fmt.wrapError",
					"*agents.TransportError",
					"invalid type",
					"*agents.ConfigError",
				},
			},
		},
	}

	unwrapped := UnwrapError(protoErr)
	localErr, ok := errors.AsType[*LocalError](unwrapped)
	require.True(t, ok)
	require.Equal(t,
		[]string{"*agents.TransportError", "*agents.ConfigError"},
		localErr.CauseTypes)
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
