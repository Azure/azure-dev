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

	err := service.AddHandler(ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{Service: service})
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
	err := service.AddHandler(ServiceEventDeploy, handler1)
	require.Nil(t, err)

	err = service.RemoveHandler(ServiceEventDeploy, handler1)
	require.Nil(t, err)

	// Handler 2 wasn't registered so should error on remove
	err = service.RemoveHandler(ServiceEventDeploy, handler2)
	require.NotNil(t, err)

	// No events are registered at the time event was raised
	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{Service: service})
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

	err := service.AddHandler(ServiceEventDeploy, handler1)
	require.Nil(t, err)
	err = service.AddHandler(ServiceEventDeploy, handler2)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{
		Project: service.Project,
		Service: service,
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

	err := service.AddHandler(ServiceEventPackage, provisionHandler)
	require.Nil(t, err)
	err = service.AddHandler(ServiceEventDeploy, deployHandler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventPackage, ServiceLifecycleEventArgs{Service: service})
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

	err := service.AddHandler(ServiceEventPackage, handler1)
	require.Nil(t, err)
	err = service.AddHandler(ServiceEventPackage, handler2)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventPackage, ServiceLifecycleEventArgs{Service: service})
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

	err := service.AddHandler(ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{Service: service})
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestServiceConfigRaiseEventWithArgs(t *testing.T) {
	ctx := context.Background()
	service := getServiceConfig()
	handlerCalled := false
	eventArgs := ServiceLifecycleEventArgs{
		Service: service,
		Args:    map[string]any{"foo": "bar"},
	}

	handler := func(ctx context.Context, eventArgs ServiceLifecycleEventArgs) error {
		handlerCalled = true
		require.Equal(t, eventArgs.Args["foo"], "bar")
		return nil
	}

	err := service.AddHandler(ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, eventArgs)
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
