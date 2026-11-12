// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type projectCapabilitiesOverride struct {
	supported bool
	err       error
}

func (o projectCapabilitiesOverride) GetAddServiceCapabilities(
	context.Context, *v1beta.EmptyRequest,
) (*v1beta.GetAddServiceCapabilitiesResponse, error) {
	return &v1beta.GetAddServiceCapabilitiesResponse{AcknowledgmentSupported: o.supported}, o.err
}

type projectOptInOverride struct {
	calls *atomic.Int64
	code  codes.Code
}

func (o projectOptInOverride) GetAddServiceCapabilities(
	context.Context, *v1beta.EmptyRequest,
) (*v1beta.GetAddServiceCapabilitiesResponse, error) {
	return &v1beta.GetAddServiceCapabilitiesResponse{AcknowledgmentSupported: true}, nil
}

func (o projectOptInOverride) AddService(
	_ context.Context, req *v1beta.AddServiceRequest,
) (*v1beta.EmptyResponse, error) {
	o.calls.Add(1)
	st := status.New(o.code, "custom mutation")
	if o.code == codes.Unimplemented {
		return nil, st.Err()
	}
	withDetails, err := st.WithDetails(&v1beta.AddServiceAcknowledgment{OperationId: req.GetOperationId()})
	if err != nil {
		return nil, err
	}
	return nil, withDetails.Err()
}

func TestServerE2E_BetaProjectCapabilitiesAreReadOnlyAndTruthful(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		override      any
		supported     bool
		code          codes.Code
		unauthorized  bool
		cancelRequest bool
		olderService  bool
	}{
		{name: "built-in", supported: true},
		{name: "unrelated Get", override: projectGetOnlyOverride{}, supported: true},
		{name: "custom AddService not opted in", override: projectGetAndAddOverride{}},
		{name: "explicit unsupported", override: projectCapabilitiesOverride{}},
		{
			name:      "custom explicit opt-in",
			override:  projectOptInOverride{calls: new(atomic.Int64), code: codes.FailedPrecondition},
			supported: true,
		},
		{name: "older stable implementation", olderService: true, code: codes.Unimplemented},
		{
			name:     "explicit Unimplemented",
			override: projectCapabilitiesOverride{err: status.Error(codes.Unimplemented, "older implementation")},
			code:     codes.Unimplemented,
		},
		{
			name:     "transient error",
			override: projectCapabilitiesOverride{err: status.Error(codes.Unavailable, "probe unavailable")},
			code:     codes.Unavailable,
		},
		{
			name:     "host cancellation",
			override: projectCapabilitiesOverride{err: context.Canceled},
			code:     codes.Canceled,
		},
		{name: "unauthenticated", unauthorized: true, code: codes.Unauthenticated},
		{name: "caller cancellation", cancelRequest: true, code: codes.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var options []ServerOption
			if test.override != nil {
				options = append(options, WithBetaServiceOverride(BetaProjectService, test.override))
			}
			if test.olderService {
				options = append(options, func(server *Server) {
					server.projectService = azdext.UnimplementedProjectServiceServer{}
				})
			}
			serverInfo, service := newProjectE2EServer(t, options...)
			var loads, saves atomic.Int64
			service.lazyAzdContext = lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
				loads.Add(1)
				return nil, errors.New("capability probe loaded the project context")
			})
			service.lazyProjectConfig = lazy.NewLazy(func() (*project.ProjectConfig, error) {
				loads.Add(1)
				return nil, errors.New("capability probe loaded the project")
			})
			service.saveProject = func(context.Context, *project.ProjectConfig, string) error {
				saves.Add(1)
				return errors.New("capability probe saved the project")
			}
			client, ctx := dialProjectSDK(t, serverInfo)
			if test.unauthorized {
				ctx = t.Context()
			}
			if test.cancelRequest {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			response, err := client.ProjectBeta().GetAddServiceCapabilities(ctx, &v1beta.EmptyRequest{})
			require.Equal(t, test.code, status.Code(err))
			if test.code == codes.OK {
				require.NoError(t, err)
				require.Equal(t, test.supported, response.GetAcknowledgmentSupported())
			} else {
				require.Error(t, err)
			}
			require.Zero(t, loads.Load())
			require.Zero(t, saves.Load())
		})
	}
}

func TestServerE2E_BetaProjectOptInMutationIsNotReplayed(t *testing.T) {
	t.Parallel()

	for _, code := range []codes.Code{codes.FailedPrecondition, codes.Unimplemented} {
		t.Run(code.String(), func(t *testing.T) {
			t.Parallel()
			var customCalls, stableCalls atomic.Int64
			custom := projectOptInOverride{calls: &customCalls, code: code}
			serverInfo, service := newProjectE2EServer(t, WithBetaServiceOverride(BetaProjectService, custom))
			service.saveProject = func(context.Context, *project.ProjectConfig, string) error {
				stableCalls.Add(1)
				return os.ErrPermission
			}
			client, ctx := dialProjectSDK(t, serverInfo)
			capabilities, err := client.ProjectBeta().GetAddServiceCapabilities(ctx, &v1beta.EmptyRequest{})
			require.NoError(t, err)
			require.True(t, capabilities.GetAcknowledgmentSupported())
			_, err = client.ProjectBeta().AddService(ctx, &v1beta.AddServiceRequest{
				Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
				OperationId: "opt-in-operation",
			}, grpc.MaxRetryRPCBufferSize(0))
			require.Equal(t, code, status.Code(err))
			require.Equal(t, int64(1), customCalls.Load())
			require.Zero(t, stableCalls.Load(), "a selected mutation must not fall back or replay")
			if code == codes.Unimplemented {
				require.Empty(t, status.Convert(err).Details())
			} else {
				require.Len(t, status.Convert(err).Details(), 1)
				ack, ok := status.Convert(err).Details()[0].(*v1beta.AddServiceAcknowledgment)
				require.True(t, ok)
				require.Equal(t, "opt-in-operation", ack.GetOperationId())
			}
		})
	}
}
