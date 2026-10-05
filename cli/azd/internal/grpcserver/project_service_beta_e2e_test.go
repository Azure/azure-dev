// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func newRealProjectServiceServer(t *testing.T) (azdext.ProjectServiceServer, *projectService) {
	t.Helper()
	dir := t.TempDir()
	azdContext := azdcontext.NewAzdContextWithDirectory(dir)
	cfg := &project.ProjectConfig{Name: "test"}
	require.NoError(t, project.Save(t.Context(), cfg, azdContext.ProjectPath()))
	server := NewProjectService(lazy.From(azdContext), nil, nil, lazy.From(cfg), nil, nil)
	ps, ok := server.(*projectService)
	require.True(t, ok)
	return server, ps
}

func newProjectE2EServer(t *testing.T, options ...ServerOption) (*ServerInfo, *projectService) {
	t.Helper()
	projectServer, ps := newRealProjectServiceServer(t)
	server := NewServer(
		projectServer,
		azdext.UnimplementedEnvironmentServiceServer{},
		azdext.UnimplementedPromptServiceServer{},
		azdext.UnimplementedUserConfigServiceServer{},
		azdext.UnimplementedDeploymentServiceServer{},
		azdext.UnimplementedEventServiceServer{},
		v1beta.UnimplementedComposeServiceServer{},
		azdext.UnimplementedWorkflowServiceServer{},
		azdext.UnimplementedExtensionServiceServer{},
		azdext.UnimplementedServiceTargetServiceServer{},
		azdext.UnimplementedFrameworkServiceServer{},
		azdext.UnimplementedContainerServiceServer{},
		azdext.UnimplementedAccountServiceServer{},
		azdext.UnimplementedAiModelServiceServer{},
		v1beta.UnimplementedCopilotServiceServer{},
		azdext.UnimplementedProvisioningServiceServer{},
		azdext.UnimplementedValidationServiceServer{},
		v1beta.UnimplementedTelemetryServiceServer{},
	)
	server.WithOptions(options...)
	serverInfo, err := server.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Stop()) })
	return serverInfo, ps
}

func dialProjectSDK(t *testing.T, serverInfo *ServerInfo) (*azdext.AzdClient, context.Context) {
	t.Helper()
	extension := &extensions.Extension{Id: "azd.internal.test", Namespace: "test"}
	accessToken, err := GenerateExtensionToken(extension, serverInfo)
	require.NoError(t, err)
	client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client, azdext.WithAccessToken(t.Context(), accessToken)
}

func TestServerE2E_BetaProjectServiceAddServiceSucceeds(t *testing.T) {
	t.Parallel()

	serverInfo, ps := newProjectE2EServer(t)
	client, ctx := dialProjectSDK(t, serverInfo)

	resp, err := client.ProjectBeta().AddService(ctx, &v1beta.AddServiceRequest{
		Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
		OperationId: "e2e-success",
	}, grpc.MaxRetryRPCBufferSize(0))
	require.NoError(t, err)
	require.NotNil(t, resp)

	cached, err := ps.lazyProjectConfig.GetValue()
	require.NoError(t, err)
	require.Contains(t, cached.Services, "api")
	azdContext, err := ps.lazyAzdContext.GetValue()
	require.NoError(t, err)
	saved, err := project.Load(t.Context(), azdContext.ProjectPath())
	require.NoError(t, err)
	require.Contains(t, saved.Services, "api")
}

func TestServerE2E_BetaProjectServiceAddServiceAcknowledgesCompletedFailure(t *testing.T) {
	t.Parallel()

	serverInfo, ps := newProjectE2EServer(t)
	ps.saveProject = func(_ context.Context, _ *project.ProjectConfig, _ string) error { return os.ErrPermission }
	client, ctx := dialProjectSDK(t, serverInfo)

	_, err := client.ProjectBeta().AddService(ctx, &v1beta.AddServiceRequest{
		Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
		OperationId: "e2e-acknowledged-failure",
	}, grpc.MaxRetryRPCBufferSize(0))
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok, "a completed failure must arrive as a real gRPC status, not a bare transport error")
	require.Len(t, st.Details(), 1)
	var acknowledgment *v1beta.AddServiceAcknowledgment
	for _, detail := range st.Details() {
		if ack, ok := detail.(*v1beta.AddServiceAcknowledgment); ok {
			acknowledgment = ack
		}
	}
	require.NotNil(t, acknowledgment, "the real wire response must carry AddServiceAcknowledgment")
	assert.Equal(t, "e2e-acknowledged-failure", acknowledgment.GetOperationId())
	cached, cacheErr := ps.lazyProjectConfig.GetValue()
	require.NoError(t, cacheErr)
	assert.NotContains(t, cached.Services, "api")
}

func TestServerE2E_BetaProjectServiceAddServiceOmitsAcknowledgmentWithoutOperationId(t *testing.T) {
	t.Parallel()

	serverInfo, ps := newProjectE2EServer(t)
	ps.saveProject = func(_ context.Context, _ *project.ProjectConfig, _ string) error { return os.ErrPermission }
	client, ctx := dialProjectSDK(t, serverInfo)

	_, err := client.ProjectBeta().AddService(ctx, &v1beta.AddServiceRequest{
		Service: &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
	})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Empty(t, st.Details(), "no operation_id was supplied; no acknowledgment detail may be attached")
}

func TestServerE2E_StableProjectServiceAddServiceUnchanged(t *testing.T) {
	t.Parallel()

	serverInfo, ps := newProjectE2EServer(t)
	ps.saveProject = func(_ context.Context, _ *project.ProjectConfig, _ string) error { return os.ErrPermission }

	client, ctx := dialProjectSDK(t, serverInfo)
	ctx = metadata.AppendToOutgoingContext(ctx, "azd-project-add-service-operation", "e2e-stable-token")
	var trailers metadata.MD
	_, err := client.Project().AddService(ctx, &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
	}, grpc.Trailer(&trailers))
	require.Error(t, err)
	assert.ErrorContains(t, err, "permission denied")
	assert.Equal(t, []string{"e2e-stable-token"}, trailers.Get("azd-project-add-service-save-failed"))
}

func TestServerE2E_BetaProjectServiceCancellationDoesNotConfirmCompletion(t *testing.T) {
	t.Parallel()

	serverInfo, ps := newProjectE2EServer(t)
	started, finish, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	t.Cleanup(release)
	ps.saveProject = func(context.Context, *project.ProjectConfig, string) error {
		close(started)
		<-finish
		close(completed)
		return os.ErrPermission
	}
	client, ctx := dialProjectSDK(t, serverInfo)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.ProjectBeta().AddService(ctx, &v1beta.AddServiceRequest{
			Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
			OperationId: "cancelled-attempt",
		}, grpc.MaxRetryRPCBufferSize(0))
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("save was not called")
	}
	cancel()
	select {
	case err := <-result:
		require.Equal(t, codes.Canceled, status.Code(err))
		assert.Empty(t, status.Convert(err).Details())
	case <-time.After(10 * time.Second):
		t.Fatal("client did not observe cancellation")
	}
	select {
	case <-completed:
		t.Fatal("save completed before it was released")
	default:
	}

	release()
	select {
	case <-completed:
	case <-time.After(10 * time.Second):
		t.Fatal("host save did not finish")
	}
}
