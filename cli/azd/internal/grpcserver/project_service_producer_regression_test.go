// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
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
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func newProducerRegressionServer(t *testing.T) (*ServerInfo, *projectService, string) {
	t.Helper()
	_, service, path := newBetaProjectServiceOverrideFixture(t)
	server := newTestServer(
		azdext.UnimplementedContainerServiceServer{}, azdext.UnimplementedExtensionServiceServer{},
	)
	server.projectService = service
	info, err := server.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Stop()) })
	return info, service, path
}

func dialProducerRegressionSDK(t *testing.T, info *ServerInfo) (*azdext.AzdClient, context.Context) {
	t.Helper()
	token, err := GenerateExtensionToken(&extensions.Extension{Id: "azd.internal.test", Namespace: "test"}, info)
	require.NoError(t, err)
	client, err := azdext.NewAzdClient(azdext.WithAddress(info.Address))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client, azdext.WithAccessToken(t.Context(), token)
}

func producerCancellationErrors() []struct {
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

func producerCancellationSave(err error) func(context.Context, *project.ProjectConfig, string) error {
	return func(ctx context.Context, _ *project.ProjectConfig, _ string) error {
		// Preserve wrapped fixtures to exercise the host's original error-chain handling.
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

func TestProducerCancellationDoesNotAcknowledge(t *testing.T) {
	t.Parallel()

	for _, beta := range []bool{false, true} {
		channel := "stable"
		if beta {
			channel = "beta"
		}
		for _, test := range producerCancellationErrors() {
			t.Run(channel+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				override, service, path := newBetaProjectServiceOverrideFixture(t)
				before, err := os.ReadFile(path)
				require.NoError(t, err)
				service.saveProject = producerCancellationSave(test.err)
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
					ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(
						"azd-project-add-service-operation", "canceled-attempt",
					))
					_, resultErr = service.AddService(ctx, &azdext.AddServiceRequest{
						Service: &azdext.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
					})
					assert.Empty(t, stream.savedFailure())
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
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, before, after)
			})
		}
	}
}

func TestProducerCancellationOverAuthenticatedWire(t *testing.T) {
	t.Parallel()

	for _, beta := range []bool{false, true} {
		channel := "stable"
		if beta {
			channel = "beta"
		}
		for _, test := range producerCancellationErrors() {
			t.Run(channel+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				info, service, _ := newProducerRegressionServer(t)
				service.saveProject = producerCancellationSave(test.err)
				client, ctx := dialProducerRegressionSDK(t, info)
				var resultErr error
				var trailers metadata.MD
				if beta {
					_, resultErr = client.ProjectBeta().AddService(ctx, &v1beta.AddServiceRequest{
						Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
						OperationId: "canceled-attempt",
					}, grpc.MaxRetryRPCBufferSize(0), grpc.Trailer(&trailers))
				} else {
					ctx = metadata.AppendToOutgoingContext(ctx,
						"azd-project-add-service-operation", "canceled-attempt",
					)
					_, resultErr = client.Project().AddService(ctx, &azdext.AddServiceRequest{
						Service: &azdext.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
					}, grpc.Trailer(&trailers))
				}
				require.Error(t, resultErr)
				require.Equal(t, test.code, status.Code(resultErr))
				require.Empty(t, status.Convert(resultErr).Details())
				require.Empty(t, trailers.Get("azd-project-add-service-save-failed"))
				require.NoError(t, ctx.Err(), "observe the host error, not a canceled client transport")
			})
		}
	}
}

func TestProducerPreservesRichErrorsOverAuthenticatedWire(t *testing.T) {
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
				Err: errors.New("save denied"), Message: "Could not save the project.",
				Suggestion: "Check access to the project directory.",
				Links: []errorhandler.ErrorLink{{
					Title: "Troubleshooting", URL: "https://aka.ms/azd-troubleshoot",
				}},
			},
		},
		{
			name: "actionable authentication error",
			err: &internal.ErrorWithSuggestion{
				Err:     fmt.Errorf("resolve account: %w", auth.ErrNoCurrentUser),
				Message: "Authentication required.", Suggestion: "Run azd auth login.",
			},
		},
		{
			name: "actionable response error with existing status",
			err: &internal.ErrorWithSuggestion{
				Err: &hostErrorChain{
					error: &azcore.ResponseError{
						ErrorCode: "AuthorizationFailed", StatusCode: http.StatusForbidden,
						RawResponse: &http.Response{
							StatusCode: http.StatusForbidden,
							Request: &http.Request{
								Method: http.MethodPut, Host: "management.azure.com",
								URL: &url.URL{Scheme: "https", Host: "management.azure.com"},
							},
						},
					},
					status: existingStatus,
				},
				Message: "The save request was rejected.", Suggestion: "Verify access to the resource.",
			},
		},
		{
			name: "relayed extension service error",
			err: fmt.Errorf("save project: %w", &azdext.ServiceError{
				Message: "The service rejected the save.", ErrorCode: "AuthorizationFailed",
				StatusCode: http.StatusForbidden, ServiceName: "management.azure.com",
				Suggestion: "Request access to the resource.",
				Links: []errorhandler.ErrorLink{{
					Title: "Troubleshooting", URL: "https://aka.ms/azd-troubleshoot",
				}},
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			info, service, _ := newProducerRegressionServer(t)
			service.saveProject = func(context.Context, *project.ProjectConfig, string) error { return test.err }
			client, ctx := dialProducerRegressionSDK(t, info)
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
			ack, ok := actual.Details()[len(actual.Details())-1].(*v1beta.AddServiceAcknowledgment)
			require.True(t, ok, "auth and permission failures still retain the approved producer acknowledgment")
			require.Equal(t, "rich-error-attempt", ack.GetOperationId())
			withoutAck := actual.Proto()
			withoutAck.Details = withoutAck.Details[:len(withoutAck.Details)-1]
			require.True(t, proto.Equal(expected.Proto(), withoutAck))
		})
	}
}

func TestProducerOperationIdentifierByteBound(t *testing.T) {
	t.Parallel()

	for _, wire := range []bool{false, true} {
		channel := "direct"
		if wire {
			channel = "wire"
		}
		for _, token := range []string{"", strings.Repeat("a", 64), strings.Repeat("\u00e9", 32),
			strings.Repeat("a", 65), strings.Repeat("\u00e9", 33)} {
			t.Run(fmt.Sprintf("%s/%d-bytes-%d-runes", channel, len(token), len([]rune(token))), func(t *testing.T) {
				t.Parallel()
				override, service, path := newBetaProjectServiceOverrideFixture(t)
				invoke := override.AddService
				ctx := t.Context()
				if wire {
					info, ps, projectPath := newProducerRegressionServer(t)
					service, path = ps, projectPath
					client, authenticated := dialProducerRegressionSDK(t, info)
					ctx = authenticated
					invoke = func(ctx context.Context, req *v1beta.AddServiceRequest) (*v1beta.EmptyResponse, error) {
						return client.ProjectBeta().AddService(ctx, req, grpc.MaxRetryRPCBufferSize(0))
					}
				}
				before, err := os.ReadFile(path)
				require.NoError(t, err)
				var saved atomic.Bool
				service.saveProject = func(context.Context, *project.ProjectConfig, string) error {
					saved.Store(true)
					return os.ErrPermission
				}
				_, err = invoke(ctx, &v1beta.AddServiceRequest{
					Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
					OperationId: token,
				})
				require.Error(t, err)
				st := status.Convert(err)
				switch {
				case len(token) > 64:
					require.Equal(t, codes.InvalidArgument, st.Code())
					require.ErrorContains(t, err, "operation_id")
					require.ErrorContains(t, err, "64 bytes")
					require.False(t, saved.Load())
					require.Empty(t, st.Details())
				case token == "":
					require.True(t, saved.Load())
					require.Empty(t, st.Details())
				default:
					require.True(t, saved.Load())
					require.Len(t, st.Details(), 1)
					ack, ok := st.Details()[0].(*v1beta.AddServiceAcknowledgment)
					require.True(t, ok)
					require.Equal(t, token, ack.GetOperationId())
				}
				cached, err := service.lazyProjectConfig.GetValue()
				require.NoError(t, err)
				require.NotContains(t, cached.Services, "api")
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		}
	}
}
