// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/test"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestProvisionInitializesEnvironment(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Select an Azure Subscription to use")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		// Select the first from the list
		return 0, nil
	})
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Select an Azure location")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		// Select the first from the list
		return 0, nil
	})

	registerContainerDependencies(mockContext, env)

	envManager := &mockenv.MockEnvManager{}
	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	require.Equal(t, "00000000-0000-0000-0000-000000000000", env.GetSubscriptionId())
	require.Equal(t, "location", env.GetLocation())
}

func TestManagerPreview(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
	})

	mockContext := mocks.NewMockContext(t.Context())
	registerContainerDependencies(mockContext, env)

	envManager := &mockenv.MockEnvManager{}
	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	deploymentPlan, err := mgr.Preview(*mockContext.Context)

	require.NotNil(t, deploymentPlan)
	require.Nil(t, err)
}

func TestManagerGetState(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
	})

	mockContext := mocks.NewMockContext(t.Context())
	registerContainerDependencies(mockContext, env)

	envManager := &mockenv.MockEnvManager{}
	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	getResult, err := mgr.State(*mockContext.Context, nil)

	require.NotNil(t, getResult)
	require.Nil(t, err)
}

func TestManagerDeploy(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
	})

	mockContext := mocks.NewMockContext(t.Context())
	registerContainerDependencies(mockContext, env)

	envManager := &mockenv.MockEnvManager{}
	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	deployResult, err := mgr.Deploy(*mockContext.Context)

	require.NotNil(t, deployResult)
	require.Nil(t, err)
}

func TestManagerDestroyWithPositiveConfirmation(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
	})

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Are you sure you want to destroy?")
	}).Respond(true)

	registerContainerDependencies(mockContext, env)

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", *mockContext.Context, env).Return(nil)

	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	destroyOptions := provisioning.NewDestroyOptions(false, false)
	destroyResult, err := mgr.Destroy(*mockContext.Context, destroyOptions)

	require.NotNil(t, destroyResult)
	require.Nil(t, err)
	require.Contains(t, mockContext.Console.Output(), "Are you sure you want to destroy?")
}

func TestManagerDestroyWithNegativeConfirmation(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
	})

	mockContext := mocks.NewMockContext(t.Context())

	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Are you sure you want to destroy?")
	}).Respond(false)

	registerContainerDependencies(mockContext, env)

	envManager := &mockenv.MockEnvManager{}
	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	destroyOptions := provisioning.NewDestroyOptions(false, false)
	destroyResult, err := mgr.Destroy(*mockContext.Context, destroyOptions)

	require.Nil(t, destroyResult)
	require.NotNil(t, err)
	require.Contains(t, mockContext.Console.Output(), "Are you sure you want to destroy?")
}

func TestEnsureSubscriptionAndLocation_NoPromptMissingSubscriptionReturnsPromptRequiredError(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)

	err := provisioning.EnsureSubscriptionAndLocation(t.Context(), &mockenv.MockEnvManager{},
		env,
		noPromptPrompter{},
		provisioning.EnsureSubscriptionAndLocationOptions{},
	)
	promptErr := requirePromptRequiredError(t, err, input.RequiredInput{
		Name: "subscription",
		Sources: []input.InputSource{
			{
				Kind: input.InputSourceEnvironment,
				Name: environment.SubscriptionIdEnvVarName,
			},
		},
	})

	require.Contains(t, promptErr.ToString(""), environment.SubscriptionIdEnvVarName)
}

func TestEnsureSubscriptionAndLocation_NoPromptMissingLocationReturnsPromptRequiredError(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, env).Return(nil).Once()

	err := provisioning.EnsureSubscriptionAndLocation(t.Context(), envManager,
		env,
		noPromptPrompter{},
		provisioning.EnsureSubscriptionAndLocationOptions{},
	)
	promptErr := requirePromptRequiredError(t, err, input.RequiredInput{
		Name: "location",
		Sources: []input.InputSource{
			{
				Kind: input.InputSourceEnvironment,
				Name: environment.LocationEnvVarName,
			},
		},
	})

	require.Contains(t, promptErr.ToString(""), environment.LocationEnvVarName)
	envManager.AssertExpectations(t)
}

func TestEnsureSubscription_NoPromptMissingSubscriptionReturnsPromptRequiredError(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)

	err := provisioning.EnsureSubscription(t.Context(), &mockenv.MockEnvManager{},
		env,
		noPromptPrompter{},
	)
	requirePromptRequiredError(t, err, input.RequiredInput{
		Name: "subscription",
		Sources: []input.InputSource{
			{
				Kind: input.InputSourceEnvironment,
				Name: environment.SubscriptionIdEnvVarName,
			},
		},
	})
}

type noPromptPrompter struct{}

func (p noPromptPrompter) PromptSubscription(ctx context.Context, msg string) (string, error) {
	panic("unexpected PromptSubscription call")
}

func (p noPromptPrompter) PromptLocation(
	ctx context.Context,
	subId string,
	msg string,
	filter prompt.LocationFilterPredicate,
	defaultLocation *string,
) (string, error) {
	panic("unexpected PromptLocation call")
}

func (p noPromptPrompter) PromptResourceGroup(ctx context.Context, options prompt.PromptResourceOptions) (string, error) {
	panic("unexpected PromptResourceGroup call")
}

func (p noPromptPrompter) PromptResourceGroupFrom(
	ctx context.Context,
	subscriptionId string,
	location string,
	options prompt.PromptResourceGroupFromOptions,
) (string, error) {
	panic("unexpected PromptResourceGroupFrom call")
}

func (p noPromptPrompter) IsNoPromptMode() bool {
	return true
}

func requirePromptRequiredError(
	t *testing.T,
	err error,
	expectedInput input.RequiredInput,
) *input.PromptRequiredError {
	t.Helper()

	promptErr, ok := errors.AsType[*input.PromptRequiredError](err)
	require.True(t, ok)
	require.Equal(t, []input.RequiredInput{expectedInput}, promptErr.Inputs)
	require.Contains(t, promptErr.ToString(""), input.DefaultPromptRequiredMessage)

	return promptErr
}

func registerContainerDependencies(mockContext *mocks.MockContext, env *environment.Environment) {
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", *mockContext.Context, env).Return(nil)

	mockContext.Container.MustRegisterSingleton(func() environment.Manager {
		return envManager
	})

	mockContext.Container.MustRegisterSingleton(func() account.SubscriptionCredentialProvider {
		return mockContext.SubscriptionCredentialProvider
	})
	mockContext.Container.MustRegisterSingleton(func() *policy.ClientOptions {
		return mockContext.ArmClientOptions
	})

	mockContext.Container.MustRegisterSingleton(azapi.NewResourceService)
	mockContext.Container.MustRegisterSingleton(func() config.UserConfigManager {
		return config.NewUserConfigManager(mockContext.ConfigManager)
	})
	mockContext.Container.MustRegisterSingleton(prompt.NewDefaultPrompter)
	mockContext.Container.MustRegisterNamedTransient(string(provisioning.Test), test.NewTestProvider)
	mockContext.Container.MustRegisterSingleton(func() account.Manager {
		return &mockaccount.MockAccountManager{
			Subscriptions: []account.Subscription{
				{
					Id:   "00000000-0000-0000-0000-000000000000",
					Name: "test",
				},
			},
			Locations: []account.Location{
				{
					Name:                "location",
					DisplayName:         "Test Location",
					RegionalDisplayName: "(US) Test Location",
				},
			},
		}
	})
	mockContext.Container.MustRegisterSingleton(func() *environment.Environment {
		return env
	})
	mockContext.Container.MustRegisterSingleton(func() *azapi.AzureClient {
		return mockazapi.NewAzureClientFromMockContext(mockContext)
	})

	mockContext.Container.MustRegisterSingleton(func() clock.Clock {
		return clock.NewMock()
	})

	mockContext.Container.MustRegisterSingleton(func() *cloud.Cloud {
		return cloud.AzurePublic()
	})
}

func defaultProvider() (provisioning.ProviderKind, error) {
	return provisioning.Bicep, nil
}

// recordingProvider records the options passed to Initialize.
type recordingProvider struct {
	initializedProvider provisioning.ProviderKind
}

func (p *recordingProvider) Name() string { return "recording" }

func (p *recordingProvider) Initialize(
	ctx context.Context, projectPath string, options provisioning.Options,
) error {
	p.initializedProvider = options.Provider
	return nil
}

func (p *recordingProvider) State(
	ctx context.Context, options *provisioning.StateOptions,
) (*provisioning.StateResult, error) {
	return nil, nil
}

func (p *recordingProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	return nil, nil
}

func (p *recordingProvider) Preview(ctx context.Context) (*provisioning.DeployPreviewResult, error) {
	return nil, nil
}

func (p *recordingProvider) Destroy(
	ctx context.Context, options provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	return nil, nil
}

func (p *recordingProvider) EnsureEnv(ctx context.Context) error { return nil }

func (p *recordingProvider) Parameters(ctx context.Context) ([]provisioning.Parameter, error) {
	return nil, nil
}

func (p *recordingProvider) PlannedOutputs(ctx context.Context) ([]provisioning.PlannedOutput, error) {
	return nil, nil
}

// An unspecified provider is resolved by the default resolver; the manager must hand the
// resolved name to Provider.Initialize (extension providers validate it).
func TestManagerForwardsResolvedProviderToInitialize(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)

	mockContext := mocks.NewMockContext(t.Context())
	registerContainerDependencies(mockContext, env)

	recording := &recordingProvider{}
	ioc.RegisterNamedInstance[provisioning.Provider](mockContext.Container, "writeback", recording)

	resolver := func() (provisioning.ProviderKind, error) {
		return provisioning.ProviderKind("writeback"), nil
	}

	envManager := &mockenv.MockEnvManager{}
	mgr := provisioning.NewManager(
		mockContext.Container,
		resolver,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)

	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{})
	require.NoError(t, err)
	require.Equal(t, provisioning.ProviderKind("writeback"), recording.initializedProvider)
}
