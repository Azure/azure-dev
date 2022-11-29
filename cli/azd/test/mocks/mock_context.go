package mocks

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	mockconfig "github.com/azure/azure-dev/cli/azd/test/mocks/config"
	mockconsole "github.com/azure/azure-dev/cli/azd/test/mocks/console"
	mockexec "github.com/azure/azure-dev/cli/azd/test/mocks/exec"
	mockhttp "github.com/azure/azure-dev/cli/azd/test/mocks/httputil"
)

type MockContext struct {
	Credentials   *MockCredentials
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

	ctx = httputil.WithHttpClient(ctx, httpClient)
	ctx = config.WithConfigManager(ctx, configManager)

	mockContext := &MockContext{
		Credentials:   &credentials,
		Context:       &ctx,
		Console:       mockConsole,
		CommandRunner: commandRunner,
		HttpClient:    httpClient,
		ConfigManager: configManager,
	}

	return mockContext
}
