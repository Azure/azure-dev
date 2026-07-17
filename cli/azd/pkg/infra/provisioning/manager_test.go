// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/policy"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/test"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

func TestRecordInfraProviderUsage(t *testing.T) {
	failingResolver := func() (provisioning.ProviderKind, error) {
		return provisioning.NotSpecified, errors.New("no default provider")
	}
	unspecifiedResolver := func() (provisioning.ProviderKind, error) {
		return provisioning.NotSpecified, nil
	}

	tests := []struct {
		name            string
		layers          []provisioning.Options
		defaultProvider provisioning.DefaultProviderResolver
		expected        []string // nil means no infra.provider attribute is recorded
	}{
		{
			name:            "single explicit bicep",
			layers:          []provisioning.Options{{Provider: provisioning.Bicep}},
			defaultProvider: defaultProvider,
			expected:        []string{"bicep"},
		},
		{
			name:            "single explicit terraform",
			layers:          []provisioning.Options{{Provider: provisioning.Terraform}},
			defaultProvider: defaultProvider,
			expected:        []string{"terraform"},
		},
		{
			name:            "unspecified resolves through default",
			layers:          []provisioning.Options{{Provider: provisioning.NotSpecified}},
			defaultProvider: defaultProvider,
			expected:        []string{"bicep"},
		},
		{
			name: "uniform provider across layers",
			layers: []provisioning.Options{
				{Provider: provisioning.Bicep},
				{Provider: provisioning.Bicep},
			},
			defaultProvider: defaultProvider,
			expected:        []string{"bicep"},
		},
		{
			name: "different providers across layers record each provider",
			layers: []provisioning.Options{
				{Provider: provisioning.Bicep},
				{Provider: provisioning.Terraform},
			},
			defaultProvider: defaultProvider,
			expected:        []string{"bicep", "terraform"},
		},
		{
			name:            "single explicit arm",
			layers:          []provisioning.Options{{Provider: provisioning.Arm}},
			defaultProvider: defaultProvider,
			expected:        []string{"arm"},
		},
		{
			name:            "custom provider is bucketed",
			layers:          []provisioning.Options{{Provider: provisioning.ProviderKind("my-extension-provider")}},
			defaultProvider: defaultProvider,
			expected:        []string{provisioning.InfraProviderCustom},
		},
		{
			name: "built-in plus custom records both",
			layers: []provisioning.Options{
				{Provider: provisioning.Bicep},
				{Provider: provisioning.ProviderKind("my-extension-provider")},
			},
			defaultProvider: defaultProvider,
			expected:        []string{"bicep", provisioning.InfraProviderCustom},
		},
		{
			name: "two distinct custom providers collapse to custom",
			layers: []provisioning.Options{
				{Provider: provisioning.ProviderKind("vendor.one")},
				{Provider: provisioning.ProviderKind("vendor.two")},
			},
			defaultProvider: defaultProvider,
			expected:        []string{provisioning.InfraProviderCustom},
		},
		{
			name:            "no layers records nothing",
			layers:          nil,
			defaultProvider: defaultProvider,
			expected:        nil,
		},
		{
			name:            "unspecified with nil resolver records nothing",
			layers:          []provisioning.Options{{Provider: provisioning.NotSpecified}},
			defaultProvider: nil,
			expected:        nil,
		},
		{
			name:            "unspecified with failing resolver records nothing",
			layers:          []provisioning.Options{{Provider: provisioning.NotSpecified}},
			defaultProvider: failingResolver,
			expected:        nil,
		},
		{
			name:            "unspecified resolving to NotSpecified records nothing",
			layers:          []provisioning.Options{{Provider: provisioning.NotSpecified}},
			defaultProvider: unspecifiedResolver,
			expected:        nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Record onto a real span captured by an in-memory recorder so the test verifies the
			// attribute lands directly on the command span (not the process-global usage bag).
			sr := tracetest.NewSpanRecorder()
			tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(sr))
			ctx, span := tp.Tracer("test").Start(t.Context(), "cmd.test")

			// RecordInfraProviderUsage only reads the manager's default provider, so the remaining
			// dependencies are intentionally nil.
			mgr := provisioning.NewManager(nil, tt.defaultProvider, nil, nil, nil, nil, nil, nil)
			mgr.RecordInfraProviderUsage(ctx, tt.layers)
			span.End()

			ended := sr.Ended()
			require.Len(t, ended, 1)

			var got []string
			var found bool
			for _, attr := range ended[0].Attributes() {
				if attr.Key == fields.InfraProviderKey.Key {
					got = attr.Value.AsStringSlice()
					found = true
				}
			}

			if tt.expected == nil {
				require.False(t, found, "expected no infra.provider attribute, got %v", got)
				return
			}

			require.True(t, found, "expected infra.provider attribute to be recorded")
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestRecordInfraProviderUsage_ResolvesDefaultOnce verifies the "default provider at most once per
// call" contract: multiple unspecified layers must all resolve through the manager's default
// provider while invoking that (potentially I/O-bound) resolver exactly once, and collapse to the
// single resolved value.
func TestRecordInfraProviderUsage_ResolvesDefaultOnce(t *testing.T) {
	var calls atomic.Int32
	countingResolver := func() (provisioning.ProviderKind, error) {
		calls.Add(1)
		return provisioning.Bicep, nil
	}

	sr := tracetest.NewSpanRecorder()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(sr))
	ctx, span := tp.Tracer("test").Start(t.Context(), "cmd.test")

	layers := []provisioning.Options{
		{Provider: provisioning.NotSpecified},
		{Provider: provisioning.NotSpecified},
		{Provider: provisioning.NotSpecified},
	}

	mgr := provisioning.NewManager(nil, countingResolver, nil, nil, nil, nil, nil, nil)
	mgr.RecordInfraProviderUsage(ctx, layers)
	span.End()

	require.Equal(t, int32(1), calls.Load(), "default provider resolver must be invoked at most once per call")

	ended := sr.Ended()
	require.Len(t, ended, 1)

	var got []string
	var found bool
	for _, attr := range ended[0].Attributes() {
		if attr.Key == fields.InfraProviderKey.Key {
			got = attr.Value.AsStringSlice()
			found = true
		}
	}

	require.True(t, found, "expected infra.provider attribute to be recorded")
	require.Equal(t, []string{"bicep"}, got)
}

// TestRecordInfraProviderUsage_DoesNotLeakToSiblingSpans is a regression test for the custom
// `workflows.up` leak: recording infra.provider must attach to the command's own span rather than
// the process-global usage bag. TelemetryMiddleware copies that bag onto every span it ends, so a
// bag-based value written by a nested `provision` step would leak onto a subsequent in-process
// `deploy` span (and could overwrite the parent up aggregate). This asserts the value lands on the
// provision span only, stays out of the global usage bag, and therefore does not reach a sibling
// deploy span even when that span is finished the way the middleware finishes it.
func TestRecordInfraProviderUsage_DoesNotLeakToSiblingSpans(t *testing.T) {
	tracing.ResetUsageAttributesForTest()
	t.Cleanup(tracing.ResetUsageAttributesForTest)

	sr := tracetest.NewSpanRecorder()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(sr))

	mgr := provisioning.NewManager(nil, func() (provisioning.ProviderKind, error) {
		return provisioning.Bicep, nil
	}, nil, nil, nil, nil, nil, nil)

	// Nested `provision` step records onto its own span.
	provisionCtx, provisionSpan := tp.Tracer("test").Start(t.Context(), "cmd.provision")
	mgr.RecordInfraProviderUsage(provisionCtx, []provisioning.Options{{Provider: provisioning.Bicep}})
	provisionSpan.End()

	// Subsequent in-process `deploy` step: the telemetry middleware finishes its span by copying
	// the process-global usage bag onto it. With span-scoped recording the bag is empty of
	// infra.provider, so nothing leaks.
	_, deploySpan := tp.Tracer("test").Start(t.Context(), "cmd.deploy")
	deploySpan.SetAttributes(tracing.GetUsageAttributes()...)
	deploySpan.End()

	byName := map[string][]attribute.KeyValue{}
	for _, s := range sr.Ended() {
		byName[s.Name()] = s.Attributes()
	}

	hasInfraProvider := func(attrs []attribute.KeyValue) bool {
		for _, a := range attrs {
			if a.Key == fields.InfraProviderKey.Key {
				return true
			}
		}
		return false
	}

	require.True(t, hasInfraProvider(byName["cmd.provision"]), "provision span should carry infra.provider")
	require.False(t, hasInfraProvider(byName["cmd.deploy"]),
		"infra.provider must not leak onto the sibling deploy span")
	require.False(t, hasInfraProvider(tracing.GetUsageAttributes()),
		"infra.provider must not be written to the process-global usage bag")
}
