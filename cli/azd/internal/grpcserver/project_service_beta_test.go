// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// newBetaProjectServiceOverrideFixture builds a *projectService backed by a real temporary
// project directory, matching the fixture style already used for the v1 metadata/trailer
// acknowledgment tests in project_service_save_test.go.
func newBetaProjectServiceOverrideFixture(t *testing.T) (*betaProjectServiceOverride, *projectService, string) {
	t.Helper()
	dir := t.TempDir()
	azdContext := azdcontext.NewAzdContextWithDirectory(dir)
	cfg := &project.ProjectConfig{Name: "test"}
	require.NoError(t, project.Save(t.Context(), cfg, azdContext.ProjectPath()))
	server := NewProjectService(lazy.From(azdContext), nil, nil, lazy.From(cfg), nil, nil)
	service, ok := server.(*projectService)
	require.True(t, ok)
	return &betaProjectServiceOverride{service: service}, service, azdContext.ProjectPath()
}

func TestBetaProjectServiceAddServiceSucceeds(t *testing.T) {
	override, service, projectPath := newBetaProjectServiceOverrideFixture(t)

	resp, err := override.AddService(t.Context(), &v1beta.AddServiceRequest{
		Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
		OperationId: "typed-attempt",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	cached, err := service.lazyProjectConfig.GetValue()
	require.NoError(t, err)
	require.Contains(t, cached.Services, "api")

	saved, err := project.Load(t.Context(), projectPath)
	require.NoError(t, err)
	assert.Equal(t, project.ServiceLanguagePython, saved.Services["api"].Language)
}

func TestBetaProjectServiceAddServiceAttachesAcknowledgmentOnCompletedFailure(t *testing.T) {
	override, service, _ := newBetaProjectServiceOverrideFixture(t)
	service.saveProject = func(context.Context, *project.ProjectConfig, string) error { return os.ErrPermission }

	_, err := override.AddService(t.Context(), &v1beta.AddServiceRequest{
		Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
		OperationId: "typed-attempt",
	})
	require.Error(t, err)
	// Attaching a gRPC status detail replaces the error with a fresh status-derived one and does
	// not preserve errors.Is against the original sentinel -- the same documented trade-off already
	// made by this package's mapHostError at the gRPC serialization boundary. The message is what
	// both a real network caller and this in-process call observe.
	assert.ErrorContains(t, err, "permission denied")

	st, ok := status.FromError(err)
	require.True(t, ok, "an attached detail must produce a real gRPC status error")
	var acknowledgment *v1beta.AddServiceAcknowledgment
	for _, detail := range st.Details() {
		if ack, ok := detail.(*v1beta.AddServiceAcknowledgment); ok {
			acknowledgment = ack
		}
	}
	require.NotNil(t, acknowledgment, "a completed failure with operation_id must attach AddServiceAcknowledgment")
	assert.Equal(t, "typed-attempt", acknowledgment.GetOperationId())
}

func TestBetaProjectServiceAddServiceOmitsAcknowledgmentWithoutOperationId(t *testing.T) {
	override, service, _ := newBetaProjectServiceOverrideFixture(t)
	service.saveProject = func(context.Context, *project.ProjectConfig, string) error { return os.ErrPermission }

	_, err := override.AddService(t.Context(), &v1beta.AddServiceRequest{
		Service: &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
	})
	require.ErrorIs(t, err, os.ErrPermission, "a caller that did not opt in must receive the original error untouched")

	_, ok := status.FromError(err)
	assert.False(t, ok, "a caller that did not opt in with operation_id must not receive any status wrapping")
}

func TestBetaProjectServiceAddServiceRejectsEmptyName(t *testing.T) {
	override, _, _ := newBetaProjectServiceOverrideFixture(t)

	_, err := override.AddService(t.Context(), &v1beta.AddServiceRequest{
		Service:     &v1beta.ServiceConfig{Name: ""},
		OperationId: "typed-attempt",
	})
	require.Error(t, err)

	// The empty-name rejection happens before the mutation lock in addService, so it must not
	// be acknowledged even though operation_id was supplied.
	st, ok := status.FromError(err)
	require.True(t, ok)
	for _, detail := range st.Details() {
		_, isAck := detail.(*v1beta.AddServiceAcknowledgment)
		assert.False(t, isAck)
	}
}

// TestRegisterBetaServicesDispatchesProjectServiceAddServiceToOverride exercises the real
// generated registration path (registerBetaServices) and confirms the v1beta ProjectService
// route is registered with the generated descriptor, and that supplying betaProjectServiceOverride
// causes the generated adapter to dispatch a real v1beta AddServiceRequest to it end-to-end --
// the mutation actually reaches the project configuration, proving the typed request was not
// silently dropped anywhere between the registered route and the shared core logic.
func TestRegisterBetaServicesDispatchesProjectServiceAddServiceToOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	azdContext := azdcontext.NewAzdContextWithDirectory(dir)
	cfg := &project.ProjectConfig{Name: "test"}
	require.NoError(t, project.Save(t.Context(), cfg, azdContext.ProjectPath()))
	projectServer := NewProjectService(lazy.From(azdContext), nil, nil, lazy.From(cfg), nil, nil)
	ps, ok := projectServer.(*projectService)
	require.True(t, ok)

	registrar := &recordingRegistrar{services: map[string]*grpc.ServiceDesc{}, implementations: map[string]any{}}
	err := registerBetaServices(registrar, stableServiceImplementations(), map[BetaService]any{
		BetaProjectService: &betaProjectServiceOverride{service: ps},
	})
	require.NoError(t, err)

	const projectServiceName = "azd.extensions.v1beta.ProjectService"
	require.Same(t, &v1beta.ProjectService_ServiceDesc, registrar.services[projectServiceName])
	adapter, ok := registrar.implementations[projectServiceName].(*betaProjectServiceAdapter)
	require.True(t, ok)

	resp, err := adapter.AddService(t.Context(), &v1beta.AddServiceRequest{
		Service:     &v1beta.ServiceConfig{Name: "api", Host: "containerapp", Language: "python"},
		OperationId: "registered-route",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	cached, err := ps.lazyProjectConfig.GetValue()
	require.NoError(t, err)
	require.Contains(t, cached.Services, "api")
}

// TestAzdextAddServiceRequestFacadeUnchanged guards that the stable AddServiceRequest facade
// type used by both the v1 handler and the beta override's transcoding target still exposes
// the fields this package depends on.
func TestAzdextAddServiceRequestFacadeUnchanged(t *testing.T) {
	req := &azdext.AddServiceRequest{Service: &azdext.ServiceConfig{Name: "api"}}
	assert.Equal(t, "api", req.GetService().GetName())
}
