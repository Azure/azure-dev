// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func TestBetaProjectAddServiceOperationIdBound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"64 bytes", strings.Repeat("a", 64)},
		{"64 UTF-8 bytes", strings.Repeat("\u00e9", 32)},
		{"65 bytes", strings.Repeat("a", 65)},
		{"66 UTF-8 bytes", strings.Repeat("\u00e9", 33)},
	}
	for _, wire := range []bool{false, true} {
		channel := "direct"
		if wire {
			channel = "wire"
		}
		for _, test := range tests {
			t.Run(channel+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				var service *projectService
				var invoke func(context.Context, *v1beta.AddServiceRequest) (*v1beta.EmptyResponse, error)
				ctx := t.Context()
				if wire {
					serverInfo, ps := newProjectE2EServer(t)
					service = ps
					client, authenticated := dialProjectSDK(t, serverInfo)
					ctx = authenticated
					invoke = func(ctx context.Context, req *v1beta.AddServiceRequest) (*v1beta.EmptyResponse, error) {
						return client.ProjectBeta().AddService(ctx, req, grpc.MaxRetryRPCBufferSize(0))
					}
				} else {
					override, ps, _ := newBetaProjectServiceOverrideFixture(t)
					service = ps
					invoke = override.AddService
				}
				azdContext, err := service.lazyAzdContext.GetValue()
				require.NoError(t, err)
				before, err := os.ReadFile(azdContext.ProjectPath())
				require.NoError(t, err)
				var saved atomic.Bool
				service.saveProject = func(context.Context, *project.ProjectConfig, string) error {
					saved.Store(true)
					return os.ErrPermission
				}
				_, err = invoke(ctx, &v1beta.AddServiceRequest{
					Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
					OperationId: test.token,
				})
				require.Error(t, err)
				st := status.Convert(err)
				switch {
				case len(test.token) > 64:
					require.Equal(t, codes.InvalidArgument, st.Code())
					require.ErrorContains(t, err, "operation_id")
					require.ErrorContains(t, err, "64 bytes")
					require.False(t, saved.Load(), "reject before save or mutation")
					require.Empty(t, st.Details())
				case test.token == "":
					require.True(t, saved.Load())
					require.Empty(t, st.Details())
				default:
					require.True(t, saved.Load())
					require.Len(t, st.Details(), 1)
					ack, ok := st.Details()[0].(*v1beta.AddServiceAcknowledgment)
					require.True(t, ok)
					require.Equal(t, test.token, ack.GetOperationId())
				}
				cached, err := service.lazyProjectConfig.GetValue()
				require.NoError(t, err)
				require.NotContains(t, cached.Services, "api")
				after, err := os.ReadFile(azdContext.ProjectPath())
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		}
	}
}

type projectGetOnlyOverride struct{}

func TestBetaProjectOverrideChainDispatchesPerMethod(t *testing.T) {
	t.Parallel()

	override, service, _ := newBetaProjectServiceOverrideFixture(t)
	service.saveProject = func(context.Context, *project.ProjectConfig, string) error {
		return os.ErrPermission
	}
	adapter := &betaProjectServiceAdapter{
		stable:   service,
		override: betaOverrideChain{nil, projectGetOnlyOverride{}, betaOverrideChain{override}},
	}
	response, err := adapter.Get(t.Context(), &v1beta.EmptyRequest{})
	require.NoError(t, err)
	require.Equal(t, "custom-project", response.GetProject().GetName())
	_, err = adapter.AddService(t.Context(), &v1beta.AddServiceRequest{
		Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
		OperationId: "nested-chain",
	})
	require.Error(t, err)
	require.Len(t, status.Convert(err).Details(), 1)
	ack, ok := status.Convert(err).Details()[0].(*v1beta.AddServiceAcknowledgment)
	require.True(t, ok)
	require.Equal(t, "nested-chain", ack.GetOperationId())
}

func (projectGetOnlyOverride) Get(context.Context, *v1beta.EmptyRequest) (*v1beta.GetProjectResponse, error) {
	return &v1beta.GetProjectResponse{Project: &v1beta.ProjectConfig{Name: "custom-project"}}, nil
}

type projectGetAndAddOverride struct {
	projectGetOnlyOverride
}

func (projectGetAndAddOverride) AddService(
	_ context.Context, req *v1beta.AddServiceRequest,
) (*v1beta.EmptyResponse, error) {
	return nil, status.Error(codes.AlreadyExists, "custom add: "+req.GetOperationId())
}

func TestServerE2E_BetaProjectFocusedOverridesCompose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		override any
		custom   bool
	}{
		{"Get only preserves built-in AddService", projectGetOnlyOverride{}, false},
		{"Get and AddService keep custom priority", projectGetAndAddOverride{}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			serverInfo, service := newProjectE2EServer(t, WithBetaServiceOverride(BetaProjectService, test.override))
			var saved atomic.Bool
			service.saveProject = func(context.Context, *project.ProjectConfig, string) error {
				saved.Store(true)
				return os.ErrPermission
			}
			client, ctx := dialProjectSDK(t, serverInfo)
			response, err := client.ProjectBeta().Get(ctx, &v1beta.EmptyRequest{})
			require.NoError(t, err)
			require.Equal(t, "custom-project", response.GetProject().GetName())
			_, err = client.ProjectBeta().AddService(ctx, &v1beta.AddServiceRequest{
				Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
				OperationId: "composed-operation",
			}, grpc.MaxRetryRPCBufferSize(0))
			require.Error(t, err)
			if test.custom {
				require.Equal(t, codes.AlreadyExists, status.Code(err))
				require.ErrorContains(t, err, "custom add: composed-operation")
				require.False(t, saved.Load())
			} else {
				require.True(t, saved.Load())
				require.Len(t, status.Convert(err).Details(), 1)
				ack, ok := status.Convert(err).Details()[0].(*v1beta.AddServiceAcknowledgment)
				require.True(t, ok)
				require.Equal(t, "composed-operation", ack.GetOperationId())
			}
		})
	}
}
