package mocks

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
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
	Credentials                    *MockCredentials
	Context                        *context.Context
	Console                        *mockinput.MockConsole
	HttpClient                     *mockhttp.MockHttpClient
	CommandRunner                  *mockexec.MockCommandRunner
	ConfigManager                  *mockconfig.MockConfigManager
	Container                      *ioc.NestedContainer
	AlphaFeaturesManager           *alpha.FeatureManager
	SubscriptionCredentialProvider *MockSubscriptionCredentialProvider
	MultiTenantCredentialProvider  *MockMultiTenantCredentialProvider
	Config                         config.Config
}

func NewMockContext(ctx context.Context) *MockContext {
	httpClient := mockhttp.NewMockHttpUtil()
	configManager := mockconfig.NewMockConfigManager()
	config := config.NewEmptyConfig()

	mockContext := &MockContext{
		Credentials:                    &MockCredentials{},
		Context:                        &ctx,
		Console:                        mockinput.NewMockConsole(),
		CommandRunner:                  mockexec.NewMockCommandRunner(),
		HttpClient:                     httpClient,
		ConfigManager:                  configManager,
		SubscriptionCredentialProvider: &MockSubscriptionCredentialProvider{},
		MultiTenantCredentialProvider:  &MockMultiTenantCredentialProvider{},
		Container:                      ioc.NewNestedContainer(nil),
		Config:                         config,
		AlphaFeaturesManager:           alpha.NewFeaturesManagerWithConfig(config),
	}

	registerCommonMocks(mockContext)

	return mockContext
}

func registerCommonMocks(mockContext *MockContext) {
	mockContext.Container.RegisterSingleton(func() ioc.ServiceLocator {
		return mockContext.Container
	})
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
	mockContext.Container.RegisterSingleton(func() config.FileConfigManager {
		return mockContext.ConfigManager
	})
	mockContext.Container.RegisterSingleton(func() *alpha.FeatureManager {
		return mockContext.AlphaFeaturesManager
	})
}
