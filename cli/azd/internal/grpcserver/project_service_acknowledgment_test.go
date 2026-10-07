// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func projectCancellationErrors() []struct {
	name string
	err  error
	code codes.Code
} {
	return []struct {
		name string
		err  error
		code codes.Code
	}{
		{"canceled", context.Canceled, codes.Canceled},
		{"deadline", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"wrapped canceled", fmt.Errorf("save: %w", context.Canceled), codes.Canceled},
		{"wrapped deadline", fmt.Errorf("save: %w", context.DeadlineExceeded), codes.DeadlineExceeded},
		{"canceled status", status.Error(codes.Canceled, "save canceled"), codes.Canceled},
		{"deadline status", status.Error(codes.DeadlineExceeded, "save timed out"), codes.DeadlineExceeded},
	}
}

func projectCancellationSave(err error) func(context.Context, *project.ProjectConfig, string) error {
	return func(ctx context.Context, _ *project.ProjectConfig, _ string) error {
		// Keep wrapped fixtures unchanged so the host still exercises error-chain handling.
		switch {
		case errors.Is(err, context.Canceled) && errors.Unwrap(err) == nil:
			ctx, cancel := context.WithCancel(ctx)
			cancel()
			return ctx.Err()
		case errors.Is(err, context.DeadlineExceeded) && errors.Unwrap(err) == nil:
			ctx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer cancel()
			return ctx.Err()
		default:
			return err
		}
	}
}

func TestProjectAddServiceCancellationDoesNotAcknowledge(t *testing.T) {
	t.Parallel()

	for _, beta := range []bool{false, true} {
		channel := "stable"
		if beta {
			channel = "beta"
		}
		for _, test := range projectCancellationErrors() {
			t.Run(channel+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				override, service, projectPath := newBetaProjectServiceOverrideFixture(t)
				before, err := os.ReadFile(projectPath)
				require.NoError(t, err)
				service.saveProject = projectCancellationSave(test.err)
				var resultErr error
				if beta {
					_, resultErr = override.AddService(t.Context(), &v1beta.AddServiceRequest{
						Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
						OperationId: "canceled-attempt",
					})
					assert.Empty(t, status.Convert(resultErr).Details())
				} else {
					stream := &projectSaveTransport{}
					ctx := grpc.NewContextWithServerTransportStream(t.Context(), stream)
					_, resultErr = service.AddService(ctx, &azdext.AddServiceRequest{
						Service: &azdext.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
					})
					assert.Empty(t, stream.trailers)
				}
				require.Error(t, resultErr)
				if errors.Is(test.err, context.Canceled) || errors.Is(test.err, context.DeadlineExceeded) {
					assert.ErrorIs(t, resultErr, test.err)
				} else {
					assert.Equal(t, test.code, status.Code(resultErr))
				}
				cached, err := service.lazyProjectConfig.GetValue()
				require.NoError(t, err)
				assert.NotContains(t, cached.Services, "api")
				after, err := os.ReadFile(projectPath)
				require.NoError(t, err)
				assert.Equal(t, before, after)
			})
		}
	}
}

func TestServerE2E_ProjectAddServiceCancellationDoesNotAcknowledge(t *testing.T) {
	t.Parallel()

	for _, beta := range []bool{false, true} {
		channel := "stable"
		if beta {
			channel = "beta"
		}
		for _, test := range projectCancellationErrors() {
			t.Run(channel+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				serverInfo, service := newProjectE2EServer(t)
				service.saveProject = projectCancellationSave(test.err)
				client, ctx := dialProjectSDK(t, serverInfo)
				var resultErr error
				var trailers metadata.MD
				if beta {
					_, resultErr = client.ProjectBeta().AddService(ctx, &v1beta.AddServiceRequest{
						Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
						OperationId: "canceled-attempt",
					}, grpc.MaxRetryRPCBufferSize(0), grpc.Trailer(&trailers))
				} else {
					_, resultErr = client.Project().AddService(ctx, &azdext.AddServiceRequest{
						Service: &azdext.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
					}, grpc.Trailer(&trailers))
				}
				require.Error(t, resultErr)
				require.Equal(t, test.code, status.Code(resultErr))
				assert.Empty(t, status.Convert(resultErr).Details())
				assert.Empty(t, trailers.Get("azd-project-add-service-save-failed"))
				require.NoError(t, ctx.Err(), "observe the host error, not cancellation of the client connection")
			})
		}
	}
}

func TestServerE2E_BetaProjectAddServicePreservesHostErrorDetails(t *testing.T) {
	t.Parallel()

	existingStatus, err := status.New(codes.PermissionDenied, "save denied").WithDetails(
		&errdetails.ErrorInfo{Reason: "existing-detail", Domain: "test"},
	)
	require.NoError(t, err)
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "actionable error",
			err: &internal.ErrorWithSuggestion{
				Err:        errors.New("save denied"),
				Message:    "Could not save the project.",
				Suggestion: "Check access to the project directory.",
				Links: []errorhandler.ErrorLink{{
					Title: "Troubleshooting",
					URL:   "https://aka.ms/azd-troubleshoot",
				}},
			},
		},
		{
			name: "actionable authentication error",
			err: &internal.ErrorWithSuggestion{
				Err:        fmt.Errorf("resolve account: %w", auth.ErrNoCurrentUser),
				Message:    "Authentication required.",
				Suggestion: "Run azd auth login.",
			},
		},
		{
			name: "actionable response error with existing status",
			err: &internal.ErrorWithSuggestion{
				Err: &hostErrorChain{
					error: &azcore.ResponseError{
						ErrorCode:  "AuthorizationFailed",
						StatusCode: http.StatusForbidden,
						RawResponse: &http.Response{
							StatusCode: http.StatusForbidden,
							Body:       io.NopCloser(strings.NewReader("")),
							Request: &http.Request{
								Method: http.MethodPut,
								Host:   "management.azure.com",
								URL:    &url.URL{Scheme: "https", Host: "management.azure.com"},
							},
						},
					},
					status: existingStatus,
				},
				Message:    "The save request was rejected.",
				Suggestion: "Verify access to the resource.",
			},
		},
		{
			name: "relayed extension service error",
			err: fmt.Errorf("save project: %w", &azdext.ServiceError{
				Message:     "The service rejected the save.",
				ErrorCode:   "AuthorizationFailed",
				StatusCode:  http.StatusForbidden,
				ServiceName: "management.azure.com",
				Suggestion:  "Request access to the resource.",
				Links: []errorhandler.ErrorLink{{
					Title: "Troubleshooting",
					URL:   "https://aka.ms/azd-troubleshoot",
				}},
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			serverInfo, service := newProjectE2EServer(t)
			service.saveProject = func(context.Context, *project.ProjectConfig, string) error {
				return test.err
			}
			client, ctx := dialProjectSDK(t, serverInfo)
			expected := status.Convert(translateBetaStatusDetails(mapHostError(test.err)))
			require.NotEmpty(t, expected.Details())

			_, err := client.ProjectBeta().AddService(ctx, &v1beta.AddServiceRequest{
				Service: &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
			}, grpc.MaxRetryRPCBufferSize(0))
			require.Error(t, err)
			require.True(t, proto.Equal(expected.Proto(), status.Convert(err).Proto()))

			_, err = client.ProjectBeta().AddService(ctx, &v1beta.AddServiceRequest{
				Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
				OperationId: "rich-error-attempt",
			}, grpc.MaxRetryRPCBufferSize(0))
			require.Error(t, err)
			actual := status.Convert(err)
			require.Equal(t, expected.Code(), actual.Code())
			require.Equal(t, expected.Message(), actual.Message())
			require.Len(t, actual.Details(), len(expected.Details())+1)
			details := actual.Details()
			acknowledgment, ok := details[len(details)-1].(*v1beta.AddServiceAcknowledgment)
			require.True(t, ok, "the only added detail must be the typed acknowledgment")
			require.Equal(t, "rich-error-attempt", acknowledgment.GetOperationId())
			withoutAcknowledgment := actual.Proto()
			withoutAcknowledgment.Details = withoutAcknowledgment.Details[:len(withoutAcknowledgment.Details)-1]
			assert.True(t, proto.Equal(expected.Proto(), withoutAcknowledgment),
				"code, message, type URLs and every existing detail must survive unchanged")
		})
	}
}
