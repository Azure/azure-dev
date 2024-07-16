package mocks

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal"
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
	"github.com/benbjohnson/clock"
	"github.com/wbreza/container/v4"
)

type MockContext struct {
	Credentials                    *MockCredentials
	Context                        *context.Context
	Console                        *mockinput.MockConsole
	HttpClient                     *mockhttp.MockHttpClient
	CoreClientOptions              *azcore.ClientOptions
	ArmClientOptions               *arm.ClientOptions
	CommandRunner                  *mockexec.MockCommandRunner
	ConfigManager                  *mockconfig.MockConfigManager
	Container                      *container.Container
	AlphaFeaturesManager           *alpha.FeatureManager
	SubscriptionCredentialProvider *MockSubscriptionCredentialProvider
	MultiTenantCredentialProvider  *MockMultiTenantCredentialProvider
	Config                         config.Config
	Clock                          *clock.Mock
}

func NewMockContext(ctx context.Context) *MockContext {
	httpClient := mockhttp.NewMockHttpUtil()
	configManager := mockconfig.NewMockConfigManager()
	config := config.NewEmptyConfig()

	clientOptions := azcore.ClientOptions{
		Transport:       httpClient,
		PerCallPolicies: []policy.Policy{NewMockUserAgentPolicy(internal.UserAgent())},
	}
	armOptions := arm.ClientOptions{ClientOptions: clientOptions}

	mockContext := &MockContext{
		Credentials:                    &MockCredentials{},
		Context:                        &ctx,
		Console:                        mockinput.NewMockConsole(),
		CommandRunner:                  mockexec.NewMockCommandRunner(),
		HttpClient:                     httpClient,
		CoreClientOptions:              &clientOptions,
		ArmClientOptions:               &armOptions,
		ConfigManager:                  configManager,
		SubscriptionCredentialProvider: &MockSubscriptionCredentialProvider{},
		MultiTenantCredentialProvider:  &MockMultiTenantCredentialProvider{},
		Container:                      container.New(),
		Config:                         config,
		AlphaFeaturesManager:           alpha.NewFeaturesManagerWithConfig(config),
		Clock:                          clock.NewMock(),
	}

	registerCommonMocks(mockContext)

	return mockContext
}

func registerCommonMocks(mockContext *MockContext) {
	container.MustRegisterSingleton(mockContext.Container, func() ioc.ServiceLocator {
		return mockContext.Container
	})
	container.MustRegisterSingleton(mockContext.Container, func() httputil.HttpClient {
		return mockContext.HttpClient
	})
	container.MustRegisterSingleton(mockContext.Container, func() exec.CommandRunner {
		return mockContext.CommandRunner
	})
	container.MustRegisterSingleton(mockContext.Container, func() input.Console {
		return mockContext.Console
	})
	container.MustRegisterSingleton(mockContext.Container, func() config.FileConfigManager {
		return mockContext.ConfigManager
	})
	container.MustRegisterSingleton(mockContext.Container, func() *alpha.FeatureManager {
		return mockContext.AlphaFeaturesManager
	})
}
