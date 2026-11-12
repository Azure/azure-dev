// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"net"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type focusedProjectGet struct{}

func (focusedProjectGet) Get(context.Context, *v1beta.EmptyRequest) (*v1beta.GetProjectResponse, error) {
	return &v1beta.GetProjectResponse{Project: &v1beta.ProjectConfig{Name: "focused"}}, nil
}

type focusedProjectAdd struct{}

func (focusedProjectAdd) AddService(context.Context, *v1beta.AddServiceRequest) (*v1beta.EmptyResponse, error) {
	return &v1beta.EmptyResponse{}, nil
}

func TestBetaProjectBuiltInCoexistsWithFocusedOverridesOverGRPC(t *testing.T) {
	for _, custom := range []any{nil, focusedProjectGet{}, focusedProjectAdd{}} {
		t.Run("", func(t *testing.T) {
			_, ps, path := newBetaProjectServiceOverrideFixture(t)
			server := newTestServer(azdext.UnimplementedContainerServiceServer{},
				azdext.UnimplementedExtensionServiceServer{},
				WithBetaServiceOverride(BetaProjectService, custom))
			server.projectService = ps
			server.grpcServer = grpc.NewServer()
			require.NoError(t, server.registerServices())
			listener := bufconn.Listen(1024 * 1024)
			go func() { _ = server.grpcServer.Serve(listener) }()
			t.Cleanup(func() {
				server.grpcServer.Stop()
				require.NoError(t, listener.Close())
			})
			connection, err := grpc.NewClient("passthrough:///project",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connection.Close()) })
			client := v1beta.NewProjectServiceClient(connection)
			capabilities, err := client.GetAddServiceCapabilities(t.Context(), &v1beta.EmptyRequest{})
			require.NoError(t, err)
			_, customAdd := custom.(focusedProjectAdd)
			assert.Equal(t, !customAdd, capabilities.GetAcknowledgmentSupported())
			if customAdd {
				return
			}
			if custom != nil {
				response, err := client.Get(t.Context(), &v1beta.EmptyRequest{})
				require.NoError(t, err)
				assert.Equal(t, "focused", response.GetProject().GetName())
			}
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			ps.saveProject = func(context.Context, *project.ProjectConfig, string) error { return os.ErrPermission }
			_, err = client.AddService(t.Context(), &v1beta.AddServiceRequest{
				Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
				OperationId: "actual-network-call",
			})
			require.ErrorContains(t, err, "permission denied")
			require.Len(t, status.Convert(err).Details(), 1)
			ack, ok := status.Convert(err).Details()[0].(*v1beta.AddServiceAcknowledgment)
			require.True(t, ok)
			assert.Equal(t, "actual-network-call", ack.GetOperationId())
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			cached, err := ps.lazyProjectConfig.GetValue()
			require.NoError(t, err)
			assert.NotContains(t, cached.Services, "api")
			ps.saveProject = project.Save
			_, err = client.AddService(t.Context(), &v1beta.AddServiceRequest{
				Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
				OperationId: "next-network-call",
			})
			require.NoError(t, err)
			saved, err := project.Load(t.Context(), path)
			require.NoError(t, err)
			assert.Contains(t, saved.Services, "api")
		})
	}
}
