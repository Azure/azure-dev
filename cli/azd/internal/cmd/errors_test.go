// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
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
					[]map[string]interface{}{
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
					[]map[string]interface{}{
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
				Details:     "Too many requests",
				ErrorCode:   "RateLimitExceeded",
				StatusCode:  429,
				ServiceName: "openai.azure.com",
			},
			wantErrReason: "ext.service.openai.429",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("openai"),
				fields.ErrorKey(fields.ServiceHost.Key).String("openai.azure.com"),
				fields.ErrorKey(fields.ServiceStatusCode.Key).Int(429),
				fields.ErrorKey(fields.ServiceErrorCode.Key).String("RateLimitExceeded"),
			},
		},
		{
			name:          "WithPromptTimeoutError",
			err:           &ux.ErrPromptTimeout{Duration: 30 * time.Second},
			wantErrReason: "user.prompt_timeout",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.PromptTimeoutSeconds.Key).Int(30),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := &mocktracing.Span{}
			MapError(tt.err, span)

			require.Equal(t, tt.wantErrReason, span.Status.Description)
			require.ElementsMatch(t, tt.wantErrDetails, span.Attributes)
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

func mustMarshalJson(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
