package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mocktracing"
	"github.com/stretchr/testify/require"
)

func Test_Telemetry_Run(t *testing.T) {
	t.Run("WithRootAction", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		options := &Options{
			CommandPath:   "azd provision",
			Name:          "provision",
			isChildAction: false,
		}
		middleware := NewTelemetryMiddleware(options)

		ran := false
		var actualContext context.Context

		nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
			ran = true
			actualContext = ctx
			return nil, nil
		}

		_, _ = middleware.Run(*mockContext.Context, nextFn)

		require.True(t, ran)
		require.NotEqual(
			t,
			*mockContext.Context,
			actualContext,
			"Context should be a different instance since telemetry creates a new context",
		)
	})

	t.Run("WithChildAction", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		options := &Options{
			CommandPath:   "azd provision",
			Name:          "provision",
			isChildAction: true,
		}
		middleware := NewTelemetryMiddleware(options)

		ran := false
		var actualContext context.Context

		nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
			ran = true
			actualContext = ctx
			return nil, nil
		}

		_, _ = middleware.Run(*mockContext.Context, nextFn)

		require.True(t, ran)
		require.NotEqual(
			t,
			*mockContext.Context,
			actualContext,
			"Context should be a different instance since telemetry creates a new context",
		)
	})
}

func Test_mapError(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantErrReason  string
		wantErrDetails map[string]interface{}
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
			wantErrDetails: map[string]interface{}{
				string(fields.ToolName):     "any",
				string(fields.ToolExitCode): 51},
		},
		{
			name: "WithArmDeploymentError",
			err: &azcli.AzureDeploymentError{
				Details: &azcli.DeploymentErrorLine{
					Code: "",
					Inner: []*azcli.DeploymentErrorLine{
						{
							Code: "Conflict",
							Inner: []*azcli.DeploymentErrorLine{
								{Code: "OutOfCapacity"},
								{Code: "RegionOutOfCapacity"},
							},
						},
						{
							Code:  "PreconditionFailed",
							Inner: []*azcli.DeploymentErrorLine{},
						},
						{
							Code: "",
							Inner: []*azcli.DeploymentErrorLine{
								{
									Code: "ServiceUnavailable",
									Inner: []*azcli.DeploymentErrorLine{
										{Code: "UnknownError"},
									},
								},
							},
						},
					},
				},
			},
			wantErrReason: "service.arm.deployment.failed",
			wantErrDetails: map[string]interface{}{
				string(fields.ServiceName): "arm",
				string(fields.ServiceErrorCode): mustMarshalJson([]interface{}{
					map[string]interface{}{
						string(fields.ErrCode):  "Conflict,PreconditionFailed",
						string(fields.ErrFrame): 0,
					},
					map[string]interface{}{
						string(fields.ErrCode):  "OutOfCapacity,RegionOutOfCapacity",
						string(fields.ErrFrame): 1,
					},
					map[string]interface{}{
						string(fields.ErrCode):  "ServiceUnavailable",
						string(fields.ErrFrame): 1,
					},
					map[string]interface{}{
						string(fields.ErrCode):  "UnknownError",
						string(fields.ErrFrame): 2,
					},
				}),
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
			wantErrDetails: map[string]interface{}{
				string(fields.ServiceName):       "arm",
				string(fields.ServiceHost):       "management.azure.com",
				string(fields.ServiceMethod):     "GET",
				string(fields.ServiceErrorCode):  "ServiceUnavailable",
				string(fields.ServiceStatusCode): 503,
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
			wantErrDetails: map[string]interface{}{
				string(fields.ServiceName):          "aad",
				string(fields.ServiceErrorCode):     "50076,50078,50079",
				string(fields.ServiceStatusCode):    "invalid_grant",
				string(fields.ServiceCorrelationId): "12345",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := &mocktracing.Span{}
			mapError(tt.err, span)

			require.Equal(t, tt.wantErrReason, span.Status.Description)

			for _, kv := range span.Attributes {
				if kv.Key == fields.ErrDetails {
					expected, err := json.Marshal(tt.wantErrDetails)
					require.NoError(t, err)
					require.JSONEq(t, string(expected), kv.Value.AsString())
				}
			}
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
