// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestServiceConfigAddHandler(t *testing.T) {
	ctx := context.Background()
	service := getServiceConfig()
	handlerCalled := false

	handler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handlerCalled = true
		return nil
	}

	err := service.AddHandler(ctx, ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestServiceConfigRemoveHandler(t *testing.T) {
	ctx := context.Background()
	service := getServiceConfig()
	handler1Called := false
	handler2Called := false

	handler1 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handler1Called = true
		return nil
	}

	handler2 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handler2Called = true
		return nil
	}

	// Only handler 1 was registered
	err := service.AddHandler(ctx, ServiceEventDeploy, handler1)
	require.Nil(t, err)

	err = service.RemoveHandler(ctx, ServiceEventDeploy, handler1)
	require.Nil(t, err)

	// Handler 2 wasn't registered so should error on remove
	err = service.RemoveHandler(ctx, ServiceEventDeploy, handler2)
	require.NotNil(t, err)

	// No events are registered at the time event was raised
	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.Nil(t, err)
	require.False(t, handler1Called)
	require.False(t, handler2Called)
}

func TestServiceConfigWithMultipleEventHandlers(t *testing.T) {
	ctx := context.Background()
	service := getServiceConfig()
	handlerCalled1 := false
	handlerCalled2 := false

	handler1 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		require.Equal(t, service.Project, args.Project)
		require.Equal(t, service, args.Service)
		handlerCalled1 = true
		return nil
	}

	handler2 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		require.Equal(t, service.Project, args.Project)
		require.Equal(t, service, args.Service)
		handlerCalled2 = true
		return nil
	}

	err := service.AddHandler(ctx, ServiceEventDeploy, handler1)
	require.Nil(t, err)
	err = service.AddHandler(ctx, ServiceEventDeploy, handler2)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{
		Project:        service.Project,
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.Nil(t, err)
	require.True(t, handlerCalled1)
	require.True(t, handlerCalled2)
}

func TestServiceConfigWithMultipleEvents(t *testing.T) {
	ctx := context.Background()
	service := getServiceConfig()

	provisionHandlerCalled := false
	deployHandlerCalled := false

	provisionHandler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		provisionHandlerCalled = true
		return nil
	}

	deployHandler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		deployHandlerCalled = true
		return nil
	}

	err := service.AddHandler(ctx, ServiceEventPackage, provisionHandler)
	require.Nil(t, err)
	err = service.AddHandler(ctx, ServiceEventDeploy, deployHandler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventPackage, ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.Nil(t, err)

	require.True(t, provisionHandlerCalled)
	require.False(t, deployHandlerCalled)
}

func TestServiceConfigWithEventHandlerErrors(t *testing.T) {
	ctx := context.Background()
	service := getServiceConfig()

	handler1 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		return errors.New("sample error 1")
	}

	handler2 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		return errors.New("sample error 2")
	}

	err := service.AddHandler(ctx, ServiceEventPackage, handler1)
	require.Nil(t, err)
	err = service.AddHandler(ctx, ServiceEventPackage, handler2)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventPackage, ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "sample error 1")
	require.Contains(t, err.Error(), "sample error 2")
}

func getServiceConfig() *ServiceConfig {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  api:
    project: src/api
    language: js
    host: containerapp
`

	mockContext := mocks.NewMockContext(context.Background())
	projectConfig, _ := Parse(*mockContext.Context, testProj)

	return projectConfig.Services["api"]
}

func TestServiceConfigRaiseEventWithoutArgs(t *testing.T) {
	ctx := context.Background()
	service := getServiceConfig()
	handlerCalled := false

	handler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handlerCalled = true
		require.Empty(t, args.Args)
		return nil
	}

	err := service.AddHandler(ctx, ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestServiceConfigRaiseEventWithArgs(t *testing.T) {
	ctx := context.Background()
	service := getServiceConfig()
	handlerCalled := false
	eventArgs := ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
		Args:           map[string]any{"foo": "bar"},
	}

	handler := func(ctx context.Context, eventArgs ServiceLifecycleEventArgs) error {
		handlerCalled = true
		require.Equal(t, eventArgs.Args["foo"], "bar")
		return nil
	}

	err := service.AddHandler(ctx, ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, eventArgs)
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestServiceConfigEventHandlerReceivesServiceContext(t *testing.T) {
	ctx := context.Background()
	service := getServiceConfig()
	handlerCalled := false

	// Create a ServiceContext with some test artifacts
	serviceContext := NewServiceContext()

	// Add test artifacts to different lifecycle stages
	restoreArtifact := &Artifact{
		Kind:         ArtifactKindDirectory,
		LocationKind: LocationKindLocal,
		Location:     "/path/to/restored/dependencies",
		Metadata:     map[string]string{"stage": "restore"},
	}

	buildArtifact := &Artifact{
		Kind:         ArtifactKindDirectory,
		LocationKind: LocationKindLocal,
		Location:     "/path/to/build/output",
		Metadata:     map[string]string{"stage": "build"},
	}

	packageArtifact := &Artifact{
		Kind:         ArtifactKindArchive,
		LocationKind: LocationKindLocal,
		Location:     "/path/to/package/app.zip",
		Metadata:     map[string]string{"stage": "package"},
	}

	err := serviceContext.Restore.Add(restoreArtifact)
	require.Nil(t, err)
	err = serviceContext.Build.Add(buildArtifact)
	require.Nil(t, err)
	err = serviceContext.Package.Add(packageArtifact)
	require.Nil(t, err)

	handler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handlerCalled = true

		// Verify ServiceContext is available
		require.NotNil(t, args.ServiceContext)

		// Verify all artifacts are accessible in the handler
		require.Len(t, args.ServiceContext.Restore, 1)
		require.Len(t, args.ServiceContext.Build, 1)
		require.Len(t, args.ServiceContext.Package, 1)

		// Verify artifact details
		restoreArtifacts := args.ServiceContext.Restore
		restoreArt, found := restoreArtifacts.FindFirst()
		require.True(t, found)
		require.Equal(t, "/path/to/restored/dependencies", restoreArt.Location)
		require.Equal(t, "restore", restoreArt.Metadata["stage"])

		buildArtifacts := args.ServiceContext.Build
		buildArt, found := buildArtifacts.FindFirst()
		require.True(t, found)
		require.Equal(t, "/path/to/build/output", buildArt.Location)
		require.Equal(t, "build", buildArt.Metadata["stage"])

		packageArtifacts := args.ServiceContext.Package
		packageArt, found := packageArtifacts.FindFirst()
		require.True(t, found)
		require.Equal(t, "/path/to/package/app.zip", packageArt.Location)
		require.Equal(t, "package", packageArt.Metadata["stage"])

		return nil
	}

	err = service.AddHandler(ctx, ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{
		Project:        service.Project,
		Service:        service,
		ServiceContext: serviceContext,
	})
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func createTestServiceConfig(path string, host ServiceTargetKind, language ServiceLanguageKind) *ServiceConfig {
	return &ServiceConfig{
		Name:         "api",
		Host:         host,
		Language:     language,
		RelativePath: filepath.Join(path),
		Project: &ProjectConfig{
			Name:            "Test-App",
			Path:            ".",
			EventDispatcher: ext.NewEventDispatcher[ProjectLifecycleEventArgs](),
		},
		EventDispatcher: ext.NewEventDispatcher[ServiceLifecycleEventArgs](),
	}
}
