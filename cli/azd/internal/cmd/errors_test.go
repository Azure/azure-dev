// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mocktracing"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func Test_MapError(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantErrReason  string
		wantErrDetails []attribute.KeyValue
	}{
		{
			name:          "WithNilError",
			err:           nil,
			wantErrReason: "internal.<nil>",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("<nil>"),
			},
		},
		{
			name:          "WithOtherError",
			err:           errors.New("something bad happened!"),
			wantErrReason: "internal.errors_errorString",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("*errors.errorString"),
			},
		},
		{
			name: "WithToolExitError",
			err: &exec.ExitError{
				Cmd:      "any",
				ExitCode: 51,
			},
			wantErrReason: "tool.any.failed",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ToolName.Key).String("any"),
				fields.ErrorKey(fields.ToolExitCode.Key).Int(51),
			},
		},
		{
			name: "WithArmDeploymentError",
			err: &azapi.AzureDeploymentError{
				Operation: azapi.DeploymentOperationDeploy,
				Details: &azapi.DeploymentErrorLine{
					Code: "",
					Inner: []*azapi.DeploymentErrorLine{
						{
							Code: "Conflict",
							Inner: []*azapi.DeploymentErrorLine{
								{Code: "OutOfCapacity"},
								{Code: "RegionOutOfCapacity"},
							},
						},
						{
							Code:  "PreconditionFailed",
							Inner: []*azapi.DeploymentErrorLine{},
						},
						{
							Code: "",
							Inner: []*azapi.DeploymentErrorLine{
								{
									Code: "ServiceUnavailable",
									Inner: []*azapi.DeploymentErrorLine{
										{Code: "UnknownError"},
									},
								},
							},
						},
					},
				},
			},
			wantErrReason: "service.arm.deployment.failed",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("arm"),
				fields.ErrorKey(fields.ServiceErrorCode.Key).String(mustMarshalJson(
					[]map[string]any{
						{
							string(fields.ErrCode.Key):  "Conflict,PreconditionFailed",
							string(fields.ErrFrame.Key): 0,
						},
						{
							string(fields.ErrCode.Key):  "OutOfCapacity,RegionOutOfCapacity",
							string(fields.ErrFrame.Key): 1,
						},
						{
							string(fields.ErrCode.Key):  "ServiceUnavailable",
							string(fields.ErrFrame.Key): 1,
						},
						{
							string(fields.ErrCode.Key):  "UnknownError",
							string(fields.ErrFrame.Key): 2,
						},
					})),
			},
		},
		{
			name: "WithArmValidationError",
			err: &azapi.AzureDeploymentError{
				Operation: azapi.DeploymentOperationValidate,
				Details: &azapi.DeploymentErrorLine{
					Code: "InvalidTemplate",
					Inner: []*azapi.DeploymentErrorLine{
						{Code: "TemplateValidationFailed"},
					},
				},
			},
			wantErrReason: "service.arm.validate.failed",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("arm"),
				fields.ErrorKey(fields.ServiceErrorCode.Key).String(mustMarshalJson(
					[]map[string]any{
						{
							string(fields.ErrCode.Key):  "InvalidTemplate",
							string(fields.ErrFrame.Key): 0,
						},
						{
							string(fields.ErrCode.Key):  "TemplateValidationFailed",
							string(fields.ErrFrame.Key): 1,
						},
					})),
			},
		},
		{
			name: "WithResponseError",
			err: &azcore.ResponseError{
				ErrorCode:  "ServiceUnavailable",
				StatusCode: 503,
				RawResponse: &http.Response{
					StatusCode: 503,
					Request: &http.Request{
						Method: "GET",
						Host:   "management.azure.com",
					},
				},
			},
			wantErrReason: "service.arm.503",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("arm"),
				fields.ErrorKey(fields.ServiceHost.Key).String("management.azure.com"),
				fields.ErrorKey(fields.ServiceMethod.Key).String("GET"),
				fields.ErrorKey(fields.ServiceErrorCode.Key).String("ServiceUnavailable"),
				fields.ErrorKey(fields.ServiceStatusCode.Key).Int(503),
			},
		},
		{
			name: "WithAuthFailedError",
			err: &auth.AuthFailedError{
				Parsed: &auth.AadErrorResponse{
					Error: "invalid_grant",
					ErrorCodes: []int{
						50076,
						50078,
						50079,
					},
					CorrelationId: "12345",
				},
			},
			wantErrReason: "service.aad.failed",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("aad"),
				fields.ErrorKey(fields.ServiceErrorCode.Key).String("50076,50078,50079"),
				fields.ErrorKey(fields.ServiceStatusCode.Key).String("invalid_grant"),
				fields.ErrorKey(fields.ServiceCorrelationId.Key).String("12345"),
			},
		},
		{
			name: "WithExtServiceError",
			err: &azdext.ServiceError{
				Message:     "Rate limit exceeded",
				ErrorCode:   "create_agent.RateLimitExceeded",
				StatusCode:  429,
				ServiceName: "openai.azure.com",
			},
			wantErrReason: "ext.service.create_agent.ratelimitexceeded",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("openai"),
				fields.ErrorKey(fields.ServiceHost.Key).String("openai.azure.com"),
				fields.ErrorKey(fields.ServiceStatusCode.Key).Int(429),
				fields.ErrorKey(fields.ServiceErrorCode.Key).String("create_agent.RateLimitExceeded"),
			},
		},
		{
			name: "WithExtServiceErrorNoServiceName",
			err: &azdext.ServiceError{
				Message:   "something failed",
				ErrorCode: "start_container.invalid_payload",
			},
			wantErrReason: "ext.service.start_container.invalid_payload",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceErrorCode.Key).String("start_container.invalid_payload"),
			},
		},
		{
			name: "WithExtServiceErrorOnlyStatusCode",
			err: &azdext.ServiceError{
				Message:     "internal server error",
				StatusCode:  500,
				ServiceName: "myproject.services.ai.azure.com",
			},
			wantErrReason: "ext.service.ai.500",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("ai"),
				fields.ErrorKey(fields.ServiceHost.Key).String("services.ai.azure.com"),
				fields.ErrorKey(fields.ServiceStatusCode.Key).Int(500),
			},
		},
		{
			name: "WithExtServiceErrorMessageOnly",
			err: &azdext.ServiceError{
				Message: "unknown failure",
			},
			wantErrReason:  "ext.service.unknown.failed",
			wantErrDetails: nil,
		},
		{
			name: "WithExtServiceErrorStatusCodeOnly",
			err: &azdext.ServiceError{
				Message:    "forbidden",
				StatusCode: 403,
			},
			wantErrReason: "ext.service.unknown.403",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceStatusCode.Key).Int(403),
			},
		},
		{
			name:           "WithContextCanceled",
			err:            context.Canceled,
			wantErrReason:  "user.canceled",
			wantErrDetails: nil,
		},
		{
			name:           "WithContextDeadlineExceeded",
			err:            context.DeadlineExceeded,
			wantErrReason:  "internal.timeout",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrNoCurrentUser",
			err:            auth.ErrNoCurrentUser,
			wantErrReason:  "auth.not_logged_in",
			wantErrDetails: nil,
		},
		{
			name:           "WithWrappedErrNoCurrentUser",
			err:            fmt.Errorf("failed to create credential: %w: %w", errors.New("inner"), auth.ErrNoCurrentUser),
			wantErrReason:  "auth.not_logged_in",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrToolExecutionDenied",
			err:            consent.ErrToolExecutionDenied,
			wantErrReason:  "user.tool_denied",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrNotRepository",
			err:            git.ErrNotRepository,
			wantErrReason:  "internal.not_git_repo",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrPreviewNotSupported",
			err:            azapi.ErrPreviewNotSupported,
			wantErrReason:  "internal.preview_not_supported",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrBindMountOperationDisabled",
			err:            provisioning.ErrBindMountOperationDisabled,
			wantErrReason:  "internal.bind_mount_disabled",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrRemoteHostIsNotAzDo",
			err:            fmt.Errorf("%w: https://dev.azure.com/org", pipeline.ErrRemoteHostIsNotAzDo),
			wantErrReason:  "internal.remote_not_azdo",
			wantErrDetails: nil,
		},
		{
			name: "WithDNSError",
			err: &net.DNSError{
				Err:  "no such host",
				Name: "management.azure.com",
			},
			wantErrReason: "internal.network",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("*net.DNSError"),
			},
		},
		{
			name:           "WithWrappedContextCanceled",
			err:            fmt.Errorf("operation failed: %w", context.Canceled),
			wantErrReason:  "user.canceled",
			wantErrDetails: nil,
		},
		{
			name:          "WithEOFError",
			err:           io.EOF,
			wantErrReason: "internal.network",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("*errors.errorString"),
			},
		},
		{
			name:          "WithUnexpectedEOFError",
			err:           io.ErrUnexpectedEOF,
			wantErrReason: "internal.network",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("*errors.errorString"),
			},
		},
		{
			name: "WithExtLocalError",
			err: &azdext.LocalError{
				Message:  "invalid manifest",
				Code:     "Invalid-Config",
				Category: azdext.LocalErrorCategoryValidation,
			},
			wantErrReason: "ext.validation.invalid_config",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ErrCategory.Key).String("validation"),
				fields.ErrorKey(fields.ErrCode.Key).String("invalid_config"),
			},
		},
		{
			name: "WithExtLocalErrorUnknownCategory",
			err: &azdext.LocalError{
				Message:  "some local failure",
				Code:     "Something-Bad",
				Category: azdext.LocalErrorCategory("custom"),
			},
			wantErrReason: "ext.local.something_bad",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ErrCategory.Key).String("local"),
				fields.ErrorKey(fields.ErrCode.Key).String("something_bad"),
			},
		},
		{
			name: "WithExtLocalErrorAuthDomain",
			err: &azdext.LocalError{
				Message:  "token expired",
				Code:     "token_expired",
				Category: azdext.LocalErrorCategoryAuth,
			},
			wantErrReason: "ext.auth.token_expired",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ErrCategory.Key).String("auth"),
				fields.ErrorKey(fields.ErrCode.Key).String("token_expired"),
			},
		},
		{
			name: "WithSuggestionWrappingResponseError",
			err: &internal.ErrorWithSuggestion{
				Err: &azcore.ResponseError{
					ErrorCode:  "QuotaExceeded",
					StatusCode: 429,
					RawResponse: &http.Response{
						StatusCode: 429,
						Request: &http.Request{
							Method: "POST",
							Host:   "management.azure.com",
						},
					},
				},
				Suggestion: "Request a quota increase in the Azure portal.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("*exported.ResponseError"),
			},
		},
		{
			name: "WithSuggestionWrappingPlainError",
			err: &internal.ErrorWithSuggestion{
				Err:        errors.New("something failed"),
				Suggestion: "Try again later.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("*errors.errorString"),
			},
		},
		// Sentinel error test cases — verify typed errors wrapped in
		// ErrorWithSuggestion produce error.suggestion ResultCode with
		// the sentinel code in error.type via classifySentinel.
		{
			name: "WithErrNoProject",
			err: &internal.ErrorWithSuggestion{
				Err:        azdcontext.ErrNoProject,
				Suggestion: "Run azd init.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("internal.no_project"),
			},
		},
		{
			name: "WithErrEnvNotFound",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"environment 'dev' does not exist: %w",
					environment.ErrNotFound),
				Suggestion: "Run azd env new.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("internal.env_not_found"),
			},
		},
		{
			name: "WithErrInfraNotProvisioned",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"run azd provision: %w",
					internal.ErrInfraNotProvisioned),
				Suggestion: "Run azd provision.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.infra_not_provisioned"),
			},
		},
		{
			name: "WithErrFromPackageWithAll",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"specify a service: %w",
					internal.ErrFromPackageWithAll),
				Suggestion: "Specify a service.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.invalid_flag_combination"),
			},
		},
		{
			name: "WithErrFromPackageNoService",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"specify a service: %w",
					internal.ErrFromPackageNoService),
				Suggestion: "Specify a service.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.invalid_flag_combination"),
			},
		},
		{
			name: "WithErrCannotChangeSubscription",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"env 'dev': %w",
					internal.ErrCannotChangeSubscription),
				Suggestion: "Run azd env new.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.cannot_change_subscription"),
			},
		},
		{
			name: "WithErrCannotChangeLocation",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"env 'dev': %w",
					internal.ErrCannotChangeLocation),
				Suggestion: "Run azd env new.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.cannot_change_location"),
			},
		},
		{
			name: "WithErrPreviewMultipleLayers",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"specify a layer: %w",
					internal.ErrPreviewMultipleLayers),
				Suggestion: "Specify a single layer.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.preview_multiple_layers"),
			},
		},
		{
			name: "WithErrNoKeyNameProvided",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrNoKeyNameProvided,
				Suggestion: "Specify a key.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("internal.invalid_args"),
			},
		},
		{
			name: "WithErrNoEnvValuesProvided",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"use key=value: %w",
					internal.ErrNoEnvValuesProvided),
				Suggestion: "Use key=value pairs.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("internal.invalid_args"),
			},
		},
		{
			name: "WithErrInvalidFlagCombination",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"cannot combine flags: %w",
					internal.ErrInvalidFlagCombination),
				Suggestion: "Choose one flag.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("internal.invalid_args"),
			},
		},
		{
			name: "WithErrKeyNotFound",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"%w: 'MY_KEY'", internal.ErrKeyNotFound),
				Suggestion: "Run azd env get-values.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.key_not_found"),
			},
		},
		{
			name: "WithErrNoEnvironmentsFound",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"create one: %w",
					internal.ErrNoEnvironmentsFound),
				Suggestion: "Run azd env new.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.no_environments_found"),
			},
		},
		{
			name: "WithErrLoginDisabledDelegatedMode",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"current mode: %w",
					internal.ErrLoginDisabledDelegatedMode),
				Suggestion: "Use delegated identity.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"auth.login_disabled_delegated"),
			},
		},
		{
			name: "WithErrBranchRequiresTemplate",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"use --template: %w",
					internal.ErrBranchRequiresTemplate),
				Suggestion: "Add --template.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("internal.invalid_args"),
			},
		},
		{
			name: "WithErrMultipleInitModes",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrMultipleInitModes,
				Suggestion: "Choose one mode.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("internal.invalid_args"),
			},
		},
		// Sentinels — batch 2 (54 bare-error fixes)
		{
			name: "WithErrNoArgsProvided",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrNoArgsProvided,
				Suggestion: "Provide required args.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("internal.invalid_args"),
			},
		},
		{
			name: "WithErrInvalidArgValue",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrInvalidArgValue,
				Suggestion: "Check the value.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("internal.invalid_args"),
			},
		},
		{
			name: "WithErrOperationCancelled",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrOperationCancelled,
				Suggestion: "Try again.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.operation_cancelled"),
			},
		},
		{
			name: "WithErrConfigKeyNotFound",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrConfigKeyNotFound,
				Suggestion: "Run azd config show.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.config_key_not_found"),
			},
		},
		// ErrExtensionNotFound: kept naked — matches real
		// usage in extensions.go:152
		{
			name:          "WithErrExtensionNotFound",
			err:           internal.ErrExtensionNotFound,
			wantErrReason: "internal.extension_not_found",
		},
		{
			name: "WithErrNoExtensionsAvailable",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrNoExtensionsAvailable,
				Suggestion: "Run azd extension list.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.no_extensions_available"),
			},
		},
		// ErrExtensionTokenFailed: kept naked with %w —
		// matches real usage in extensions.go:233
		{
			name: "WithErrExtensionTokenFailed",
			err: fmt.Errorf(
				"generating token: %w",
				internal.ErrExtensionTokenFailed),
			wantErrReason: "internal.extension_error",
		},
		{
			name: "WithErrServiceNotFound",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrServiceNotFound,
				Suggestion: "Check azure.yaml.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.service_not_found"),
			},
		},
		{
			name: "WithErrResourceNotConfigured",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrResourceNotConfigured,
				Suggestion: "Run azd provision.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.resource_not_found"),
			},
		},
		{
			name: "WithErrValidationFailed",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrValidationFailed,
				Suggestion: "Fix validation errors.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.validation_failed"),
			},
		},
		{
			name: "WithErrUnsupportedOperation",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrUnsupportedOperation,
				Suggestion: "Check supported options.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String(
					"internal.unsupported_operation"),
			},
		},
		{
			name: "WithErrMcpToolsLoadFailed",
			err: &internal.ErrorWithSuggestion{
				Err:        internal.ErrMcpToolsLoadFailed,
				Suggestion: "Check MCP config.",
			},
			wantErrReason: "error.suggestion",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("internal.mcp_error"),
			},
		},
	}
	// Test cases that intentionally produce errors_errorString (the catch-all bucket).
	// Any NEW test case that produces this is a signal that a typed sentinel is needed.
	allowedCatchAll := map[string]bool{
		"WithNilError":   true,
		"WithOtherError": true,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := &mocktracing.Span{}
			MapError(tt.err, span)

			require.Equal(t, tt.wantErrReason, span.Status.Description)
			require.ElementsMatch(t, tt.wantErrDetails, span.Attributes)

			// Enforcement: no test case should produce the opaque errors_errorString
			// unless explicitly allowed. This catches regressions where new error paths
			// return bare errors.New() without typed sentinels.
			if !allowedCatchAll[tt.name] {
				require.NotContains(t, span.Status.Description, "errors_errorString",
					"test case %q produces opaque errors_errorString — use a typed sentinel error instead", tt.name)
			}
		})
	}
}

// TestMapError_ErrorWithSuggestionSetsErrorType verifies that ErrorWithSuggestion errors
// are classified as "error.suggestion" in telemetry, with the inner error type
// recorded in the error.type span attribute for detailed analysis.
// When the inner error matches a known sentinel, the descriptive code is used
// instead of the raw Go type name.
func TestMapError_ErrorWithSuggestionSetsErrorType(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantErrCode string
		wantErrType string
	}{
		{
			name: "Sentinel_uses_descriptive_code",
			err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"key not found: %w",
					internal.ErrKeyNotFound),
				Suggestion: "Run 'azd env get-values'.",
			},
			wantErrCode: "error.suggestion",
			wantErrType: "internal.key_not_found",
		},
		{
			name: "ResponseError_falls_back_to_go_type",
			err: &internal.ErrorWithSuggestion{
				Err: &azcore.ResponseError{
					ErrorCode:  "QuotaExceeded",
					StatusCode: 429,
					RawResponse: &http.Response{
						StatusCode: 429,
						Request: &http.Request{
							Method: "POST",
							Host:   "management.azure.com",
						},
					},
				},
				Suggestion: "Request a quota increase.",
			},
			wantErrCode: "error.suggestion",
			wantErrType: "*exported.ResponseError",
		},
		{
			name: "PlainError_falls_back_to_go_type",
			err: &internal.ErrorWithSuggestion{
				Err:        errors.New("unknown failure"),
				Suggestion: "Try again.",
			},
			wantErrCode: "error.suggestion",
			wantErrType: "*errors.errorString",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := &mocktracing.Span{}
			MapError(tt.err, span)

			require.Equal(t, tt.wantErrCode, span.Status.Description,
				"ErrorWithSuggestion should produce error.suggestion")

			wantAttr := fields.ErrType.String(tt.wantErrType)
			require.Contains(t, span.Attributes, wantAttr,
				"error.type attribute should record the inner error type")
		})
	}
}

func Test_cmdAsName(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{"WithNilCmd", "", ""},
		{"WithDot", ".", ""},
		{"WithFile", "TOOL", "tool"},
		{"WithFileAndExt", "tool.exe", "tool"},
		{"WithPath", "../tool.exe", "tool"},
		{"WithHiddenFile", ".TOOL", "tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, cmdAsName(tt.cmd))
		})
	}
}

func Test_normalizeCodeSegment(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback string
		want     string
	}{
		{"Empty", "", "fallback", "fallback"},
		{"Whitespace", "   ", "fallback", "fallback"},
		{"Simple", "invalid_config", "fallback", "invalid_config"},
		{"MixedCase", "RateLimitExceeded", "fallback", "ratelimitexceeded"},
		{"Hyphens", "Invalid-Config", "fallback", "invalid_config"},
		{"DotSeparated", "create_agent.NotFound", "fallback", "create_agent.notfound"},
		{"SpecialChars", "err@code!here", "fallback", "err_code_here"},
		{"OnlySpecialChars", "@#$", "fallback", "fallback"},
		{"HyphensAndDots", "my-service.rate-limit", "fallback", "my_service.rate_limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, normalizeCodeSegment(tt.value, tt.fallback))
		})
	}
}

func Test_errorType(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "NilError",
			err:  nil,
			want: "<nil>",
		},
		{
			name: "SimpleError",
			err:  errors.New("simple error"),
			want: "*errors.errorString",
		},
		{
			name: "SingleUnwrapError",
			err: &exec.ExitError{
				Cmd:      "test",
				ExitCode: 1,
			},
			want: "*exec.ExitError",
		},
		{
			name: "NestedUnwrapError",
			err: func() error {
				inner := errors.New("inner error")
				return &singleUnwrapError{
					msg: "wrapped error",
					err: inner,
				}
			}(),
			want: "*errors.errorString",
		},
		{
			name: "MultipleUnwrapErrors",
			err: func() error {
				err1 := errors.New("error 1")
				err2 := errors.New("error 2")
				return &multiUnwrapError{
					errs: []error{err1, err2},
				}
			}(),
			want: "*errors.errorString,*errors.errorString",
		},
		{
			name: "MultipleUnwrapErrorsWithNil",
			err: func() error {
				err1 := errors.New("error 1")
				return &multiUnwrapError{
					errs: []error{err1, nil, errors.New("error 2")},
				}
			}(),
			want: "*errors.errorString,*errors.errorString",
		},
		{
			name: "UnwrapReturnsNil",
			err: &singleUnwrapError{
				msg: "test error",
				err: nil,
			},
			want: "*cmd.singleUnwrapError",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errorType(tt.err)
			require.Equal(t, tt.want, got)
		})
	}
}

// Test helper types for errorType tests
type singleUnwrapError struct {
	msg string
	err error
}

func (e *singleUnwrapError) Error() string {
	return e.msg
}

func (e *singleUnwrapError) Unwrap() error {
	return e.err
}

type multiUnwrapError struct {
	errs []error
}

func (e *multiUnwrapError) Error() string {
	return "multiple errors"
}

func (e *multiUnwrapError) Unwrap() []error {
	return e.errs
}

func mustMarshalJson(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func Test_isNetworkError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "NilError",
			err:  nil,
			want: false,
		},
		{
			name: "PlainError",
			err:  errors.New("something broke"),
			want: false,
		},
		{
			name: "DNSError",
			err:  &net.DNSError{Err: "no such host", Name: "example.com"},
			want: true,
		},
		{
			name: "WrappedDNSError",
			err:  fmt.Errorf("request failed: %w", &net.DNSError{Err: "no such host", Name: "example.com"}),
			want: true,
		},
		{
			name: "EOF",
			err:  io.EOF,
			want: true,
		},
		{
			name: "UnexpectedEOF",
			err:  io.ErrUnexpectedEOF,
			want: true,
		},
		{
			name: "WrappedEOF",
			err:  fmt.Errorf("reading response: %w", io.EOF),
			want: true,
		},
		{
			name: "ContextCanceled",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "NetOpError",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
			want: true,
		},
		{
			name: "TLSRecordHeaderError",
			err:  &tls.RecordHeaderError{Msg: "bad record"},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isNetworkError(tt.err))
		})
	}
}

// Test_PackageLevelErrorsMapped scans the azd codebase for package-level error variables
// (var Err* = errors.New/fmt.Errorf) and verifies that each is either mapped in MapError (errors.go)
// or explicitly excluded.
//
// This prevents new error variables from silently falling through to the unhelpful
// "internal.errors_errorString" default in telemetry.
//
// If this test fails, you need to either:
// 1. Add an errors.Is() check for your error variable in MapError (errors.go), OR
// 2. Add it to the excludedErrors list below with a comment explaining why.
func Test_PackageLevelErrorsMapped(t *testing.T) {
	// Package-level error variables that are intentionally NOT mapped in MapError, with reasons:
	//nolint:gosec // G101: map variable name, not credentials
	excludedErrors := map[string]string{
		// Internal-only errors that never propagate to command-level
		"ErrDuplicateRegistration": "internal/mapper: programming error, not a runtime user error",
		"ErrInvalidRegistration":   "internal/mapper: programming error, not a runtime user error",
		"ErrNodeNotFound":          "pkg/yamlnode: internal YAML traversal error, always caught before command level",
		"ErrNodeWrongKind":         "pkg/yamlnode: internal YAML traversal error, always caught before command level",
		"ErrPropertyNotFound":      "pkg/tools/maven: internal property lookup, always caught before command level",
		"ErrResolveInstance":       "pkg/ioc: dependency injection error, caught during container resolution",
		"ErrInvalidEvent":          "pkg/ext: lifecycle event error, caught in event dispatcher",
		"ErrScriptTypeUnknown":     "pkg/ext: hook script validation, caught before command level",
		"ErrRunRequired":           "pkg/ext: hook configuration validation, caught before command level",
		"ErrUnsupportedScriptType": "pkg/ext: hook script validation, caught before command level",

		// Errors that are always caught/handled before reaching MapError
		"ErrEnsureEnvPreReqBicepCompileFailed": "caught in cmd/env.go and cmd/up.go before reaching telemetry",
		"ErrAzdOperationsNotEnabled":           "caught in pkg/project/dotnet_importer.go before reaching telemetry",
		"ErrAzCliSecretNotFound":               "caught in pkg/cmdsubst before reaching telemetry",
		"ErrNoSuchRemote":                      "caught in pkg/pipeline/pipeline_manager.go before reaching telemetry",
		"ErrRemoteHostIsNotGitHub":             "caught in pkg/pipeline and pkg/github before reaching telemetry",
		"ErrSSHNotSupported":                   "only defined, referenced via ErrRemoteHostIsNotAzDo flow",
		"ErrDeploymentNotFound":                "caught in provisioning/deployment callers before reaching telemetry",
		"ErrDeploymentsNotFound":               "caught in infra callers before reaching telemetry",
		"ErrDeploymentResourcesNotFound":       "caught in infra callers before reaching telemetry",
		"ErrContainerNotFound":                 "caught in storage blob callers before reaching telemetry",
		"ErrPlatformNotSupported":              "caught in platform config resolver before reaching telemetry",
		"ErrPlatformConfigNotFound":            "caught in platform config resolver before reaching telemetry",
		"ErrNoDefaultService":                  "caught in project manager callers before reaching telemetry",
		"ErrSourceNotFound":                    "caught in template source manager before reaching telemetry",
		"ErrSourceExists":                      "caught in template source manager before reaching telemetry",
		"ErrSourceTypeInvalid":                 "caught in template source manager before reaching telemetry",
		"ErrRepositoryNameInUse":               "caught in pipeline config flow before reaching telemetry",
		"ErrResourceNotFound":                  "caught in kubectl callers before reaching telemetry",
		"ErrResourceNotReady":                  "caught in kubectl callers before reaching telemetry",

		// Duplicate definitions (same error variable defined in multiple packages)
		"ErrDebuggerAborted": "defined in both cmd/middleware and pkg/azdext, handled at debug middleware level",

		// Agent consent errors that map to user-initiated cancellation
		"ErrSamplingDenied":       "agent consent: similar to user.canceled, low frequency",
		"ErrElicitationDenied":    "agent consent: similar to user.canceled, low frequency",
		"ErrToolExecutionSkipped": "agent consent: user chose to skip tool, agent continues with other tools",

		// UX cancellation that is always joined with context.Canceled (already mapped as user.canceled)
		"ErrCancelled": "pkg/ux: always errors.Join'd with ctx.Err(), caught by context.Canceled check",

		// Environment management errors surfaced as user-facing messages with suggestions
		"ErrExists":                     "environment: user-facing with suggestion, wrapped before reaching telemetry",
		"ErrNameNotSpecified":           "environment: user-facing with suggestion, wrapped before reaching telemetry",
		"ErrDefaultEnvironmentNotFound": "environment: user-facing with suggestion, wrapped before reaching telemetry",

		// Storage/auth errors caught in data store callers
		"ErrAccessDenied":     "storage blob: caught in environment data store callers before reaching telemetry",
		"ErrInvalidContainer": "storage blob: caught in environment data store callers before reaching telemetry",

		// AI model/quota errors caught in extension callers
		"ErrQuotaLocationRequired": "pkg/ai: caught in AI extension callers before reaching telemetry",
		"ErrModelNotFound":         "pkg/ai: caught in AI extension callers before reaching telemetry",
		"ErrNoDeploymentMatch":     "pkg/ai: caught in AI extension callers before reaching telemetry",

		// Auth errors that could propagate but are rare edge cases
		"ErrAzCliNotLoggedIn":         "pkg/azapi: az CLI auth delegation, wrapped by auth.Manager",
		"ErrAzCliRefreshTokenExpired": "pkg/azapi: az CLI auth delegation, wrapped by auth.Manager",

		// Pipeline/CI errors handled in pipeline config flow
		"ErrAuthNotSupported": "pkg/pipeline: caught in pipeline config flow before reaching telemetry",

		// Resource selection errors surfaced as user-facing prompts
		"ErrNoResourcesFound":   "pkg/prompt: interactive prompt error, caught in command callers",
		"ErrNoResourceSelected": "pkg/prompt: interactive prompt error, caught in command callers",

		// Template errors caught in init flow
		"ErrTemplateNotFound": "pkg/templates: caught in init/template callers before reaching telemetry",

		// GitHub CLI errors caught in pipeline config
		"ErrGitHubCliNotLoggedIn": "pkg/tools/github: caught in pipeline config flow before reaching telemetry",
		"ErrUserNotAuthorized":    "pkg/tools/github: caught in pipeline config flow before reaching telemetry",

		// Extension management errors caught in extension callers
		"ErrExtensionNotFound":          "pkg/extensions: caught in extension manager callers",
		"ErrInstalledExtensionNotFound": "pkg/extensions: caught in extension manager callers",
		"ErrRegistryExtensionNotFound":  "pkg/extensions: caught in extension manager callers",
	}

	// Find the azd root directory (two levels up from internal/cmd)
	azdRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	// Read errors.go to get the list of error variable references
	errorsGoPath := filepath.Join(azdRoot, "internal", "cmd", "errors.go")
	errorsGoContent, err := os.ReadFile(errorsGoPath)
	require.NoError(t, err)
	errorsGoStr := string(errorsGoContent)

	var unmapped []string

	// Walk the source tree and parse each Go file using go/ast to find
	// package-level var declarations (including var blocks) of the form:
	//   var ErrX = errors.New(...)
	//   var ErrX = fmt.Errorf(...)
	err = filepath.Walk(azdRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || base == "extensions" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil // skip unparseable files
		}

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.VAR {
				continue
			}

			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}

				for i, name := range valueSpec.Names {
					if !strings.HasPrefix(name.Name, "Err") {
						continue
					}

					if i >= len(valueSpec.Values) {
						continue
					}

					// Check if the value is errors.New(...) or fmt.Errorf(...)
					callExpr, ok := valueSpec.Values[i].(*ast.CallExpr)
					if !ok {
						continue
					}

					if !isErrorConstructorCall(callExpr) {
						continue
					}

					errVarName := name.Name

					if _, ok := excludedErrors[errVarName]; ok {
						continue
					}

					if !strings.Contains(errorsGoStr, errVarName) {
						relPath, _ := filepath.Rel(azdRoot, path)
						unmapped = append(unmapped, fmt.Sprintf("  %s (defined in %s)", errVarName, relPath))
					}
				}
			}
		}

		return nil
	})
	require.NoError(t, err)

	if len(unmapped) > 0 {
		t.Errorf(
			"Found %d package-level error variable(s) not mapped in MapError (internal/cmd/errors.go).\n"+
				"Each error variable should have an errors.Is() check in MapError for meaningful telemetry,\n"+
				"or be added to excludedErrors in this test with a reason.\n\n"+
				"Unmapped errors:\n%s",
			len(unmapped),
			strings.Join(unmapped, "\n"),
		)
	}
}

// isErrorConstructorCall checks if a call expression is errors.New(...) or fmt.Errorf(...).
func isErrorConstructorCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	return (ident.Name == "errors" && sel.Sel.Name == "New") ||
		(ident.Name == "fmt" && sel.Sel.Name == "Errorf")
}

// Test_RunMethodsNoBareErrors walks all action Run() methods and flags inline bare errors
// (errors.New or fmt.Errorf without %w) that would produce opaque errors_errorString in telemetry.
// These errors reach MapError via the telemetry middleware and must use typed sentinels or wrap
// an existing error with %w for proper classification.
func Test_RunMethodsNoBareErrors(t *testing.T) {
	azdRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	// knownBareErrors tracks pre-existing bare error violations in Run() methods.
	// This list should be EMPTY — all bare errors should use typed sentinels.
	// If a new bare error must be temporarily added, it requires justification.
	knownBareErrors := map[string]bool{}

	var violations []string
	var knownFound int

	err = filepath.Walk(azdRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || base == "extensions" || base == ".git" || base == "test" {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}

		for _, decl := range file.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}

			if !isActionRunMethod(funcDecl) {
				continue
			}

			relPath, _ := filepath.Rel(azdRoot, path)
			receiverName := getReceiverTypeName(funcDecl)

			// Walk the function body looking for return statements with bare errors
			ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
				retStmt, ok := n.(*ast.ReturnStmt)
				if !ok || len(retStmt.Results) < 2 {
					return true
				}

				// The error is the last return value
				errExpr := retStmt.Results[len(retStmt.Results)-1]

				// Check for direct errors.New(...) or fmt.Errorf() without %w
				if call, ok := errExpr.(*ast.CallExpr); ok {
					if isBareErrorCall(call) {
						pos := fset.Position(retStmt.Pos())
						key := fmt.Sprintf("%s:%d", relPath, pos.Line)
						if knownBareErrors[key] {
							knownFound++
						} else {
							violations = append(violations, fmt.Sprintf(
								"  %s %s.Run() — bare %s (use a typed sentinel with %%w)",
								key, receiverName, callName(call)))
						}
					}
				}

				// Check for &ErrorWithSuggestion{Err: errors.New(...)} or similar
				// where the inner Err field is a bare error
				if unary, ok := errExpr.(*ast.UnaryExpr); ok {
					if comp, ok := unary.X.(*ast.CompositeLit); ok {
						checkCompositeLitForBareErr(
							fset, comp, relPath, receiverName, knownBareErrors, &knownFound, &violations)
					}
				}

				return true
			})
		}

		return nil
	})
	require.NoError(t, err)

	if len(violations) > 0 {
		t.Errorf(
			"Found %d NEW bare error(s) in action Run() methods that would produce opaque telemetry.\n"+
				"Use typed sentinel errors (internal.ErrXxx) with %%w wrapping, or wrap with an existing\n"+
				"typed error from a dependency. Bare errors.New()/fmt.Errorf() without %%w produce\n"+
				"'internal.errors_errorString' in telemetry.\n\n"+
				"Violations:\n%s",
			len(violations),
			strings.Join(violations, "\n"),
		)
	}

	// Verify allowlist isn't stale — if a known error was fixed, remove it from the list
	if knownFound != len(knownBareErrors) {
		t.Errorf(
			"knownBareErrors allowlist has %d entries but only %d were found.\n"+
				"Remove fixed entries from the allowlist to keep it accurate.",
			len(knownBareErrors), knownFound)
	}
}

// isActionRunMethod checks if a function declaration is a method named "Run" that returns
// (*actions.ActionResult, error) — the signature of azd action entry points.
func isActionRunMethod(fn *ast.FuncDecl) bool {
	if fn.Name.Name != "Run" {
		return false
	}
	// Must be a method (have a receiver)
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return false
	}
	// Must have parameters (ctx context.Context)
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return false
	}
	// Must return two values where the second is error
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 2 {
		return false
	}

	// Check first return type contains "ActionResult" (pointer to it)
	firstResult := fn.Type.Results.List[0]
	if star, ok := firstResult.Type.(*ast.StarExpr); ok {
		if sel, ok := star.X.(*ast.SelectorExpr); ok {
			if sel.Sel.Name != "ActionResult" {
				return false
			}
		} else {
			return false
		}
	} else {
		return false
	}

	// Check second return type is "error"
	secondResult := fn.Type.Results.List[1]
	if ident, ok := secondResult.Type.(*ast.Ident); ok {
		if ident.Name != "error" {
			return false
		}
	} else {
		return false
	}

	return true
}

// getReceiverTypeName extracts the receiver type name from a method declaration.
func getReceiverTypeName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return "<unknown>"
	}
	switch t := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return "<unknown>"
}

// isBareErrorCall checks if a call expression is errors.New(...) or fmt.Errorf(...) without %w.
func isBareErrorCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	// errors.New(...) is always bare
	if ident.Name == "errors" && sel.Sel.Name == "New" {
		return true
	}

	// fmt.Errorf(...) is bare only if format string has no %w
	if ident.Name == "fmt" && sel.Sel.Name == "Errorf" {
		return !fmtErrorfHasWrap(call)
	}

	return false
}

// fmtErrorfHasWrap checks if a fmt.Errorf call contains %w in the format string.
func fmtErrorfHasWrap(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok {
		return false
	}
	return strings.Contains(lit.Value, "%w")
}

// callName returns a human-readable name for a call expression (e.g. "errors.New" or "fmt.Errorf").
func callName(call *ast.CallExpr) string {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if ident, ok := sel.X.(*ast.Ident); ok {
			return ident.Name + "." + sel.Sel.Name
		}
	}
	return "<call>"
}

// checkCompositeLitForBareErr checks if a composite literal (e.g., ErrorWithSuggestion{})
// has an Err field set to a bare error.
func checkCompositeLitForBareErr(
	fset *token.FileSet,
	comp *ast.CompositeLit,
	relPath, receiverName string,
	knownBareErrors map[string]bool,
	knownFound *int,
	violations *[]string,
) {
	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		keyIdent, ok := kv.Key.(*ast.Ident)
		if !ok || keyIdent.Name != "Err" {
			continue
		}
		if call, ok := kv.Value.(*ast.CallExpr); ok {
			if isBareErrorCall(call) {
				pos := fset.Position(comp.Pos())
				key := fmt.Sprintf("%s:%d", relPath, pos.Line)
				if knownBareErrors[key] {
					(*knownFound)++
				} else {
					*violations = append(*violations, fmt.Sprintf(
						"  %s %s.Run() — ErrorWithSuggestion wrapping bare %s (use a typed sentinel with %%w)",
						key, receiverName, callName(call)))
				}
			}
		}
	}
}
