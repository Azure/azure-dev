package mocks

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/alphafeatures"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockconfig"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

type MockContext struct {
	Credentials          *MockCredentials
	Context              *context.Context
	Console              *mockinput.MockConsole
	HttpClient           *mockhttp.MockHttpClient
	CommandRunner        *mockexec.MockCommandRunner
	ConfigManager        *mockconfig.MockConfigManager
	Container            *ioc.NestedContainer
	AlphaFeaturesManager *alphafeatures.AlphaFeatureManager
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
		Container:     ioc.NewNestedContainer(nil),
	}

	registerCommonMocks(mockContext)

	return mockContext
}

func registerCommonMocks(mockContext *MockContext) {
	mockContext.Container.RegisterSingleton(func() azcore.TokenCredential {
		return mockContext.Credentials
	})
	mockContext.Container.RegisterSingleton(func() httputil.HttpClient {
		return mockContext.HttpClient
	})
	mockContext.Container.RegisterSingleton(func() exec.CommandRunner {
		return mockContext.CommandRunner
	})
	mockContext.Container.RegisterSingleton(func() input.Console {
		return mockContext.Console
	})
	mockContext.Container.RegisterSingleton(func() config.Manager {
		return mockContext.ConfigManager
	})
	mockContext.Container.RegisterSingleton(func() *alphafeatures.AlphaFeatureManager {
		return mockContext.AlphaFeaturesManager
	})
}
