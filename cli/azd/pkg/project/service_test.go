package project

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/helpers"
	"github.com/stretchr/testify/require"
)

var (
	projectYaml = `
name: test-proj
metadata:
  template: test-project
resourceGroup: test-resource-group-name
services:
  api:
    project: src/api
    language: js
    host: appservice
`
	env             = &environment.Environment{}
	deploymentScope = environment.NewDeploymentScope("test-subscription-id", "test-resource-group-name", "test-resource-name")
	mockEndpoints   = []string{"https://test-resource.azurewebsites.net"}
)

type mockFrameworkService struct {
}

func (st *mockFrameworkService) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (st *mockFrameworkService) Package(ctx context.Context, progress chan<- string) (string, error) {
	progress <- "mock package progress"
	return "", nil
}

func (st *mockFrameworkService) InstallDependencies(ctx context.Context) error {
	return nil
}

func (st *mockFrameworkService) Initialize(ctx context.Context) error {
	return nil
}

type mockServiceTarget struct {
}

func (st *mockServiceTarget) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (st *mockServiceTarget) Deploy(_ context.Context, _ *azdcontext.AzdContext, _ string, progress chan<- string) (ServiceDeploymentResult, error) {
	progress <- "mock deploy progress"
	return ServiceDeploymentResult{
		TargetResourceId: "target-resource-id",
		Kind:             AppServiceTarget,
		Details:          "",
		Endpoints:        mockEndpoints,
	}, nil
}

func (st *mockServiceTarget) Endpoints(_ context.Context) ([]string, error) {
	return mockEndpoints, nil
}

func TestDeployProgressMessages(t *testing.T) {
	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)

	projectConfig, _ := ParseProjectConfig(projectYaml, env)
	project, _ := projectConfig.GetProject(ctx, env)
	azdContext, _ := azdcontext.NewAzdContext()

	mockFramework := &mockFrameworkService{}
	mockTarget := &mockServiceTarget{}

	service := Service{
		Project:   project,
		Config:    project.Config.Services["api"],
		Framework: mockFramework,
		Target:    mockTarget,
		Scope:     deploymentScope,
	}

	result, progress := service.Deploy(ctx, azdContext)
	progressMessages := []string{}

	go func() {
		for message := range progress {
			progressMessages = append(progressMessages, message)
		}
	}()

	deployResponse := <-result
	require.NotNil(t, deployResponse)

	require.True(t, arrayContains(progressMessages, "mock package progress"))
	require.True(t, arrayContains(progressMessages, "mock deploy progress"))
	require.Equal(t, deployResponse.Result.Endpoints, mockEndpoints)
}

func arrayContains(arr []string, value string) bool {
	for _, v := range arr {
		if v == value {
			return true
		}
	}

	return false
}
