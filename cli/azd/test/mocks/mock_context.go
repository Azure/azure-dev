package mocks

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockconfig"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

type MockContext struct {
	Credentials   *MockCredentials
	Context       *context.Context
	Console       *mockinput.MockConsole
	HttpClient    *mockhttp.MockHttpClient
	CommandRunner *mockexec.MockCommandRunner
	ConfigManager *mockconfig.MockConfigManager
}

func NewMockContext(ctx context.Context) *MockContext {
	mockConsole := mockinput.NewMockConsole()
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
