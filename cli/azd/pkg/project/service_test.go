package project

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
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
	mockTarget = environment.NewTargetResource(
		"test-subscription-id",
		"test-resource-group-name",
		"test-resource-name",
		"resource/type",
	)
	mockEndpoints = []string{"https://test-resource.azurewebsites.net"}
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

func (st *mockServiceTarget) Deploy(
	_ context.Context,
	_ *azdcontext.AzdContext,
	_ string,
	progress chan<- string,
) (ServiceDeploymentResult, error) {
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
	mockContext := mocks.NewMockContext(context.Background())
	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		convert.RefOf("test-resource-group-name"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       convert.RefOf("test-api"),
				Name:     convert.RefOf("test-api"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Location: convert.RefOf("eastus2"),
				Tags: map[string]*string{
					defaultServiceTag: convert.RefOf("api"),
				},
			},
		},
	)
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	env := environment.Ephemeral()
	env.SetSubscriptionId("SUBSCRIPTION_ID")

	projectConfig, _ := ParseProjectConfig(projectYaml)
	project, _ := projectConfig.GetProject(*mockContext.Context, env, mockContext.Console, azCli, mockContext.CommandRunner)
	azdContext, _ := azdcontext.NewAzdContext()

	mockFramework := &mockFrameworkService{}
	mockServiceTarget := &mockServiceTarget{}

	service := Service{
		Project:        project,
		Config:         project.Config.Services["api"],
		Environment:    env,
		Framework:      mockFramework,
		Target:         mockServiceTarget,
		TargetResource: mockTarget,
	}

	result, progress := service.Deploy(*mockContext.Context, azdContext)
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
