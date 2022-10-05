package mocks

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	mockconsole "github.com/azure/azure-dev/cli/azd/test/mocks/console"
	mockexec "github.com/azure/azure-dev/cli/azd/test/mocks/exec"
	mockhttp "github.com/azure/azure-dev/cli/azd/test/mocks/httputil"
)

type MockContext struct {
	Context       *context.Context
	Console       *mockconsole.MockConsole
	HttpClient    *mockhttp.MockHttpClient
	CommandRunner *mockexec.MockCommandRunner
}

func NewMockContext(ctx context.Context) *MockContext {
	mockConsole := mockconsole.NewMockConsole()
	commandRunner := mockexec.NewMockCommandRunner()
	httpClient := mockhttp.NewMockHttpUtil()
	credentials := MockCredentials{}

	mockexec.AddAzLoginMocks(commandRunner)
	httpClient.AddDefaultMocks()

	ctx = internal.WithCommandOptions(ctx, internal.GlobalCommandOptions{})
	ctx = input.WithConsole(ctx, mockConsole)
	ctx = exec.WithCommandRunner(ctx, commandRunner)
	ctx = httputil.WithHttpClient(ctx, httpClient)
	ctx = identity.WithCredentials(ctx, &credentials)

	mockContext := &MockContext{
		Context:       &ctx,
		Console:       mockConsole,
		CommandRunner: commandRunner,
		HttpClient:    httpClient,
	}

	return mockContext
}
