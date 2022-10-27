package mocks

import (
	"context"
	"os"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	mockconfig "github.com/azure/azure-dev/cli/azd/test/mocks/config"
	mockconsole "github.com/azure/azure-dev/cli/azd/test/mocks/console"
	mockexec "github.com/azure/azure-dev/cli/azd/test/mocks/exec"
	mockhttp "github.com/azure/azure-dev/cli/azd/test/mocks/httputil"
)

type MockContext struct {
	Context       *context.Context
	Console       *mockconsole.MockConsole
	HttpClient    *mockhttp.MockHttpClient
	CommandRunner *mockexec.MockCommandRunner
	ConfigManager *mockconfig.MockConfigManager
}

func NewMockContext(ctx context.Context) *MockContext {
	mockConsole := mockconsole.NewMockConsole()
	commandRunner := mockexec.NewMockCommandRunner()
	httpClient := mockhttp.NewMockHttpUtil()
	credentials := MockCredentials{}
	configManager := mockconfig.NewMockConfigManager()

	mockexec.AddAzLoginMocks(commandRunner)
	httpClient.AddDefaultMocks()

	ctx = internal.WithCommandOptions(ctx, internal.GlobalCommandOptions{})
	ctx = input.WithConsole(ctx, mockConsole)
	ctx = exec.WithCommandRunner(ctx, commandRunner)
	ctx = httputil.WithHttpClient(ctx, httpClient)
	ctx = identity.WithCredentials(ctx, &credentials)
	ctx = output.WithWriter(ctx, os.Stdout)

	mockContext := &MockContext{
		Context:       &ctx,
		Console:       mockConsole,
		CommandRunner: commandRunner,
		HttpClient:    httpClient,
		ConfigManager: configManager,
	}

	return mockContext
}
