package project

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
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

	e := environment.EphemeralWithValues("test-env", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := ParseProjectConfig(testProj)
	require.Nil(t, err)
	require.NotNil(t, projectConfig)

	require.Equal(t, "test-proj", projectConfig.Name)
	require.Equal(t, "test-proj-template", projectConfig.Metadata.Template)
	require.Equal(t, fmt.Sprintf("rg-%s", e.GetEnvName()), projectConfig.ResourceGroupName.MustEnvsubst(e.Getenv))
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

	projectConfig, err := ParseProjectConfig(testProj)
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
	mockContext := mocks.NewMockContext(context.Background())
	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		convert.RefOf("rg-test"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       convert.RefOf("test-api"),
				Name:     convert.RefOf("test-api"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Location: convert.RefOf("eastus"),
				Tags: map[string]*string{
					defaultServiceTag: convert.RefOf("api"),
				},
			},
			{
				ID:       convert.RefOf("test-web"),
				Name:     convert.RefOf("test-web"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Location: convert.RefOf("eastus"),
				Tags: map[string]*string{
					defaultServiceTag: convert.RefOf("web"),
				},
			},
		},
	)
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	e := environment.EphemeralWithValues("test-env", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := ParseProjectConfig(testProj)
	require.Nil(t, err)

	project, err := projectConfig.GetProject(*mockContext.Context, e, mockContext.Console, azCli, mockContext.CommandRunner)
	require.Nil(t, err)
	require.NotNil(t, project)

	require.Same(t, projectConfig, project.Config)

	for _, svc := range project.Services {
		require.Same(t, project, svc.Project)
		require.NotNil(t, svc.Config)
		require.NotNil(t, svc.Framework)
		require.NotNil(t, svc.Target)
		require.NotNil(t, svc.TargetResource)
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

	projectConfig, err := ParseProjectConfig(testProj)

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

	projectConfig, err := ParseProjectConfig(testProj)

	require.NotNil(t, projectConfig)
	require.Nil(t, err)

	service := projectConfig.Services["api"]

	require.Equal(t, "./api/api", service.Module)
}

func TestProjectConfigAddHandler(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
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

	err = project.RaiseEvent(*mockContext.Context, Deployed, nil)
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestProjectConfigRemoveHandler(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
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
	err = project.RaiseEvent(*mockContext.Context, Deployed, nil)
	require.Nil(t, err)
	require.False(t, handler1Called)
	require.False(t, handler2Called)
}

func TestProjectConfigWithMultipleEventHandlers(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
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

	err = project.RaiseEvent(*mockContext.Context, Deployed, nil)
	require.Nil(t, err)
	require.True(t, handlerCalled1)
	require.True(t, handlerCalled2)
}

func TestProjectConfigWithMultipleEvents(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
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

	err = project.RaiseEvent(*mockContext.Context, Provisioned, nil)
	require.Nil(t, err)

	require.True(t, provisionHandlerCalled)
	require.False(t, deployHandlerCalled)
}

func TestProjectConfigWithEventHandlerErrors(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
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

	err = project.RaiseEvent(*mockContext.Context, Provisioned, nil)
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

	projectConfig, _ := ParseProjectConfig(testProj)

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
	mockContext := mocks.NewMockContext(context.Background())
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

	err = project.RaiseEvent(*mockContext.Context, Deployed, eventArgs)
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestExpandableStringsInProjectConfig(t *testing.T) {

	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: ${foo}
services:
  api:
    project: src/api
    language: js
    host: containerapp
    module: ./api/api
    `

	projectConfig, err := ParseProjectConfig(testProj)
	require.NoError(t, err)

	env := environment.EphemeralWithValues("", map[string]string{
		"foo": "hello",
		"bar": "goodbye",
	})

	require.Equal(t, "hello", projectConfig.ResourceGroupName.MustEnvsubst(env.Getenv))
}
