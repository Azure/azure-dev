package project

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/helpers"
	"github.com/stretchr/testify/require"
)

func TestProjectConfigDefaults(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test-env
services:
  web:
    project: src/web
    language: js
    host: appservice
  api:
    project: src/api
    language: js
    host: appservice
`

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("test-env")

	projectConfig, err := ParseProjectConfig(testProj, &e)
	require.Nil(t, err)
	require.NotNil(t, projectConfig)

	require.Equal(t, "test-proj", projectConfig.Name)
	require.Equal(t, "test-proj-template", projectConfig.Metadata.Template)
	require.Equal(t, fmt.Sprintf("rg-%s", e.GetEnvName()), projectConfig.ResourceGroupName)
	require.Equal(t, 2, len(projectConfig.Services))

	for key, svc := range projectConfig.Services {
		require.Equal(t, key, svc.Module)
		require.Equal(t, key, svc.Name)
		require.Equal(t, projectConfig, svc.Project)
	}
}

func TestProjectConfigHasService(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  web:
    project: src/web
    language: js
    host: appservice
  api:
    project: src/api
    language: js
    host: appservice
`

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("test-env")

	projectConfig, err := ParseProjectConfig(testProj, &e)
	require.Nil(t, err)

	require.True(t, projectConfig.HasService("web"))
	require.True(t, projectConfig.HasService("api"))
	require.False(t, projectConfig.HasService("foobar"))
}

func TestProjectConfigGetProject(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  web:
    project: src/web
    language: js
    host: appservice
  api:
    project: src/api
    language: js
    host: appservice
`

	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)
	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("test-env")

	projectConfig, err := ParseProjectConfig(testProj, &e)
	require.Nil(t, err)

	project, err := projectConfig.GetProject(ctx, &e)
	require.Nil(t, err)
	require.NotNil(t, project)

	require.Same(t, projectConfig, project.Config)

	for _, svc := range project.Services {
		require.Same(t, project, svc.Project)
		require.NotNil(t, svc.Config)
		require.NotNil(t, svc.Framework)
		require.NotNil(t, svc.Target)
		require.NotNil(t, svc.Scope)
	}
}

func TestProjectWithCustomDockerOptions(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  web:
    project: src/web
    language: js
    host: containerapp
    docker:
      path: ./Dockerfile.dev
      context: ../
`

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("test-env")

	projectConfig, err := ParseProjectConfig(testProj, &e)

	require.NotNil(t, projectConfig)
	require.Nil(t, err)

	service := projectConfig.Services["web"]

	require.Equal(t, "./Dockerfile.dev", service.Docker.Path)
	require.Equal(t, "../", service.Docker.Context)
}

func TestProjectWithCustomModule(t *testing.T) {
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

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("test-env")

	projectConfig, err := ParseProjectConfig(testProj, &e)

	require.NotNil(t, projectConfig)
	require.Nil(t, err)

	service := projectConfig.Services["api"]

	require.Equal(t, "./api/api", service.Module)
}

func TestProjectConfigAddHandler(t *testing.T) {
	ctx := context.Background()
	project := getProjectConfig()
	handlerCalled := false

	handler := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handlerCalled = true
		return nil
	}

	err := project.AddHandler(Deployed, handler)
	require.Nil(t, err)

	// Expected error if attempting to register the same handler more than 1 time
	err = project.AddHandler(Deployed, handler)
	require.NotNil(t, err)

	err = project.RaiseEvent(ctx, Deployed, nil)
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestProjectConfigRemoveHandler(t *testing.T) {
	ctx := context.Background()
	project := getProjectConfig()
	handler1Called := false
	handler2Called := false

	handler1 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handler1Called = true
		return nil
	}

	handler2 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handler2Called = true
		return nil
	}

	// Only handler 1 was registered
	err := project.AddHandler(Deployed, handler1)
	require.Nil(t, err)

	err = project.RemoveHandler(Deployed, handler1)
	require.Nil(t, err)

	// Handler 2 wasn't registered so should error on remove
	err = project.RemoveHandler(Deployed, handler2)
	require.NotNil(t, err)

	// No events are registered at the time event was raised
	err = project.RaiseEvent(ctx, Deployed, nil)
	require.Nil(t, err)
	require.False(t, handler1Called)
	require.False(t, handler2Called)
}

func TestProjectConfigWithMultipleEventHandlers(t *testing.T) {
	ctx := context.Background()
	project := getProjectConfig()
	handlerCalled1 := false
	handlerCalled2 := false

	handler1 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		require.Equal(t, project, args.Project)
		handlerCalled1 = true
		return nil
	}

	handler2 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		require.Equal(t, project, args.Project)
		handlerCalled2 = true
		return nil
	}

	err := project.AddHandler(Deployed, handler1)
	require.Nil(t, err)
	err = project.AddHandler(Deployed, handler2)
	require.Nil(t, err)

	err = project.RaiseEvent(ctx, Deployed, nil)
	require.Nil(t, err)
	require.True(t, handlerCalled1)
	require.True(t, handlerCalled2)
}

func TestProjectConfigWithMultipleEvents(t *testing.T) {
	ctx := context.Background()
	project := getProjectConfig()

	provisionHandlerCalled := false
	deployHandlerCalled := false

	provisionHandler := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		provisionHandlerCalled = true
		return nil
	}

	deployHandler := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		deployHandlerCalled = true
		return nil
	}

	err := project.AddHandler(Provisioned, provisionHandler)
	require.Nil(t, err)
	err = project.AddHandler(Deployed, deployHandler)
	require.Nil(t, err)

	err = project.RaiseEvent(ctx, Provisioned, nil)
	require.Nil(t, err)

	require.True(t, provisionHandlerCalled)
	require.False(t, deployHandlerCalled)
}

func TestProjectConfigWithEventHandlerErrors(t *testing.T) {
	ctx := context.Background()
	project := getProjectConfig()

	handler1 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		return errors.New("sample error 1")
	}

	handler2 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		return errors.New("sample error 2")
	}

	err := project.AddHandler(Provisioned, handler1)
	require.Nil(t, err)
	err = project.AddHandler(Provisioned, handler2)
	require.Nil(t, err)

	err = project.RaiseEvent(ctx, Provisioned, nil)
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "sample error 1")
	require.Contains(t, err.Error(), "sample error 2")
}

func getProjectConfig() *ProjectConfig {
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

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("test-env")

	projectConfig, _ := ParseProjectConfig(testProj, &e)

	return projectConfig
}

func TestProjectConfigRaiseEventWithoutArgs(t *testing.T) {
	ctx := context.Background()
	project := getProjectConfig()
	handlerCalled := false

	handler := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handlerCalled = true
		require.Empty(t, args.Args)
		return nil
	}

	err := project.AddHandler(Deployed, handler)
	require.Nil(t, err)

	// Expected error if attempting to register the same handler more than 1 time
	err = project.AddHandler(Deployed, handler)
	require.NotNil(t, err)

	err = project.RaiseEvent(ctx, Deployed, nil)
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestProjectConfigRaiseEventWithArgs(t *testing.T) {
	ctx := context.Background()
	project := getProjectConfig()
	handlerCalled := false
	eventArgs := make(map[string]any)
	eventArgs["foo"] = "bar"

	handler := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handlerCalled = true
		require.Equal(t, args.Args["foo"], "bar")
		return nil
	}

	err := project.AddHandler(Deployed, handler)
	require.Nil(t, err)

	// Expected error if attempting to register the same handler more than 1 time
	err = project.AddHandler(Deployed, handler)
	require.NotNil(t, err)

	err = project.RaiseEvent(ctx, Deployed, eventArgs)
	require.Nil(t, err)
	require.True(t, handlerCalled)
}
