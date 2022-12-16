package project

import (
	"context"
	"errors"
	"testing"

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

	err := service.AddHandler(Deployed, handler)
	require.Nil(t, err)

	// Expected error if attempting to register the same handler more than 1 time
	err = service.AddHandler(Deployed, handler)
	require.NotNil(t, err)

	err = service.RaiseEvent(ctx, Deployed, nil)
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
	err := service.AddHandler(Deployed, handler1)
	require.Nil(t, err)

	err = service.RemoveHandler(Deployed, handler1)
	require.Nil(t, err)

	// Handler 2 wasn't registered so should error on remove
	err = service.RemoveHandler(Deployed, handler2)
	require.NotNil(t, err)

	// No events are registered at the time event was raised
	err = service.RaiseEvent(ctx, Deployed, nil)
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

	err := service.AddHandler(Deployed, handler1)
	require.Nil(t, err)
	err = service.AddHandler(Deployed, handler2)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, Deployed, nil)
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

	err := service.AddHandler(Provisioned, provisionHandler)
	require.Nil(t, err)
	err = service.AddHandler(Deployed, deployHandler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, Provisioned, nil)
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

	err := service.AddHandler(Provisioned, handler1)
	require.Nil(t, err)
	err = service.AddHandler(Provisioned, handler2)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, Provisioned, nil)
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
    module: ./api/api
`

	projectConfig, _ := ParseProjectConfig(testProj)

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

	err := service.AddHandler(Deployed, handler)
	require.Nil(t, err)

	// Expected error if attempting to register the same handler more than 1 time
	err = service.AddHandler(Deployed, handler)
	require.NotNil(t, err)

	err = service.RaiseEvent(ctx, Deployed, nil)
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestServiceConfigRaiseEventWithArgs(t *testing.T) {
	ctx := context.Background()
	service := getServiceConfig()
	handlerCalled := false
	eventArgs := make(map[string]any)
	eventArgs["foo"] = "bar"

	handler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handlerCalled = true
		require.Equal(t, args.Args["foo"], "bar")
		return nil
	}

	err := service.AddHandler(Deployed, handler)
	require.Nil(t, err)

	// Expected error if attempting to register the same handler more than 1 time
	err = service.AddHandler(Deployed, handler)
	require.NotNil(t, err)

	err = service.RaiseEvent(ctx, Deployed, eventArgs)
	require.Nil(t, err)
	require.True(t, handlerCalled)
}
