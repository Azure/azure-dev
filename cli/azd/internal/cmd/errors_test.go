package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
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
			name:           "WithNilError",
			err:            nil,
			wantErrReason:  "UnknownError",
			wantErrDetails: nil,
		},
		{
			name:           "WithOtherError",
			err:            errors.New("something bad happened!"),
			wantErrReason:  "UnknownError",
			wantErrDetails: nil,
		},
		{
			name: "WithToolExitError",
			err: &exec.ExitError{
				Cmd:      "any",
				ExitCode: 51,
			},
			wantErrReason: "tool.any.failed",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ToolName).String("any"),
				fields.ErrorKey(fields.ToolExitCode).Int(51),
			},
		},
		{
			name: "WithArmDeploymentError",
			err: &azapi.AzureDeploymentError{
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
				fields.ErrorKey(fields.ServiceName).String("arm"),
				fields.ErrorKey(fields.ServiceErrorCode).String(mustMarshalJson(
					[]map[string]interface{}{
						{
							string(fields.ErrCode):  "Conflict,PreconditionFailed",
							string(fields.ErrFrame): 0,
						},
						{
							string(fields.ErrCode):  "OutOfCapacity,RegionOutOfCapacity",
							string(fields.ErrFrame): 1,
						},
						{
							string(fields.ErrCode):  "ServiceUnavailable",
							string(fields.ErrFrame): 1,
						},
						{
							string(fields.ErrCode):  "UnknownError",
							string(fields.ErrFrame): 2,
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
				fields.ErrorKey(fields.ServiceName).String("arm"),
				fields.ErrorKey(fields.ServiceHost).String("management.azure.com"),
				fields.ErrorKey(fields.ServiceMethod).String("GET"),
				fields.ErrorKey(fields.ServiceErrorCode).String("ServiceUnavailable"),
				fields.ErrorKey(fields.ServiceStatusCode).Int(503),
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
				fields.ErrorKey(fields.ServiceName).String("aad"),
				fields.ErrorKey(fields.ServiceErrorCode).String("50076,50078,50079"),
				fields.ErrorKey(fields.ServiceStatusCode).String("invalid_grant"),
				fields.ErrorKey(fields.ServiceCorrelationId).String("12345"),
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

func mustMarshalJson(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
