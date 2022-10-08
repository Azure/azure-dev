package mocks

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	azsdkMock "github.com/azure/azure-dev/cli/azd/test/mocks/azsdk"
	mockconsole "github.com/azure/azure-dev/cli/azd/test/mocks/console"
	mockexec "github.com/azure/azure-dev/cli/azd/test/mocks/exec"
	mockhttp "github.com/azure/azure-dev/cli/azd/test/mocks/httputil"
)

type MockContext struct {
	Context         *context.Context
	Console         *mockconsole.MockConsole
	HttpClient      *mockhttp.MockHttpClient
	CommandRunner   *mockexec.MockCommandRunner
	MockDeployments *azsdkMock.MockDeploymentClient
}

func NewMockContext(ctx context.Context) *MockContext {
	mockConsole := mockconsole.NewMockConsole()
	commandRunner := mockexec.NewMockCommandRunner()
	httpClient := mockhttp.NewMockHttpUtil()
	deploymentMock := &azsdkMock.MockDeploymentClient{}

	mockexec.AddAzLoginMocks(commandRunner)
	httpClient.AddDefaultMocks()

	ctx = internal.WithCommandOptions(ctx, internal.GlobalCommandOptions{})
	ctx = input.WithConsole(ctx, mockConsole)
	ctx = exec.WithCommandRunner(ctx, commandRunner)
	ctx = httputil.WithHttpClient(ctx, httpClient)
	ctx = azsdk.WithDeploymentFactory(ctx, func(subscriptionId string, credential azcore.TokenCredential) (azsdk.DeploymentClient, error) {
		return deploymentMock, nil
	})

	mockContext := &MockContext{
		Context:         &ctx,
		Console:         mockConsole,
		CommandRunner:   commandRunner,
		HttpClient:      httpClient,
		MockDeployments: deploymentMock,
	}

	return mockContext
}
