// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning_test

import (
	"context"
	"errors"
	"fmt"
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

	envManager := registerContainerDependencies(mockContext, env)
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
	envManager := registerContainerDependencies(mockContext, env)
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
	envManager := registerContainerDependencies(mockContext, env)
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
	envManager := registerContainerDependencies(mockContext, env)
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

	envManager := registerContainerDependencies(mockContext, env)

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

	envManager := registerContainerDependencies(mockContext, env)
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

type mappedOutputEnvironment struct {
	environment.ScopedEnvironment
	logicalKey  string
	physicalKey string
}

func (e *mappedOutputEnvironment) DotenvDelete(key string) {
	if key == e.logicalKey {
		key = e.physicalKey
	}

	e.ScopedEnvironment.DotenvDelete(key)
}

type destroyResultProvider struct {
	provisioning.Provider
	result *provisioning.DestroyResult
}

type mappedEnvironmentProvider struct {
	provisioning.Provider
	env              environment.ScopedEnvironment
	initializedInput string
}

type aliasTestProvider struct {
	provisioning.Provider
	options    provisioning.Options
	parameters []provisioning.Parameter
	outputs    []provisioning.PlannedOutput
	deployment *provisioning.Deployment
	err        error
}

func (p *aliasTestProvider) Initialize(_ context.Context, _ string, options provisioning.Options) error {
	p.options = options
	return nil
}

func (p *aliasTestProvider) Parameters(context.Context) ([]provisioning.Parameter, error) {
	return p.parameters, p.err
}

func (p *aliasTestProvider) PlannedOutputs(context.Context) ([]provisioning.PlannedOutput, error) {
	return p.outputs, p.err
}

func (p *aliasTestProvider) Deploy(context.Context) (*provisioning.DeployResult, error) {
	return &provisioning.DeployResult{Deployment: p.deployment}, p.err
}

func TestManagerPipelineMetadataUsesProjectView(t *testing.T) {
	t.Parallel()

	provider := &aliasTestProvider{
		parameters: []provisioning.Parameter{
			{Name: "plain", Value: "value", EnvVarMapping: []string{"PROVIDER_VARIABLE", "UNCHANGED"},
				UsingEnvVarMapping: true},
			{Name: "secret", Secret: true, Value: "secret", EnvVarMapping: []string{"PROVIDER_VARIABLE"}, LocalPrompt: true},
			{Name: "overlapping", EnvVarMapping: []string{"PROJECT_VARIABLE"}},
			{Name: "unmapped"},
		},
		outputs: []provisioning.PlannedOutput{{Name: "PROVIDER_VARIABLE"}, {Name: "PROJECT_VARIABLE"}, {Name: "UNCHANGED"}},
	}
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Test), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, nil, environment.New("test"),
		mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Test,
		// PROJECT_VARIABLE is also a provider-view name here; each alias must be applied only once.
		ParamAliases: map[string]string{
			"PROVIDER_VARIABLE": "PROJECT_VARIABLE",
			"PROJECT_VARIABLE":  "OTHER_PROJECT_VARIABLE",
		},
		OutputAliases: map[string]string{
			"PROVIDER_VARIABLE": "PROJECT_VARIABLE",
			"PROJECT_VARIABLE":  "OTHER_PROJECT_VARIABLE",
		},
	}))

	parameters, err := manager.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, []provisioning.Parameter{
		{Name: "plain", Value: "value", EnvVarMapping: []string{"PROJECT_VARIABLE", "UNCHANGED"},
			UsingEnvVarMapping: true},
		{Name: "secret", Secret: true, Value: "secret", EnvVarMapping: []string{"PROJECT_VARIABLE"}, LocalPrompt: true},
		{Name: "overlapping", EnvVarMapping: []string{"OTHER_PROJECT_VARIABLE"}},
		{Name: "unmapped"},
	}, parameters)

	outputs, err := manager.PlannedOutputs(t.Context())
	require.NoError(t, err)
	require.Equal(t, []provisioning.PlannedOutput{
		{Name: "PROJECT_VARIABLE"}, {Name: "OTHER_PROJECT_VARIABLE"}, {Name: "UNCHANGED"},
	}, outputs)
	require.Equal(t, []string{"PROVIDER_VARIABLE", "UNCHANGED"}, provider.parameters[0].EnvVarMapping)
	require.Equal(t, "PROVIDER_VARIABLE", provider.outputs[0].Name)

	provider.err = errors.New("provider metadata failed")
	_, err = manager.Parameters(t.Context())
	require.ErrorIs(t, err, provider.err)
	_, err = manager.PlannedOutputs(t.Context())
	require.ErrorIs(t, err, provider.err)

	provider.err = nil
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{Provider: provisioning.Test}))
	parameters, err = manager.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, provider.parameters, parameters)
	outputs, err = manager.PlannedOutputs(t.Context())
	require.NoError(t, err)
	require.Equal(t, provider.outputs, outputs)
}

func TestManagerVirtualInputsUseProviderView(t *testing.T) {
	t.Parallel()

	provider := &aliasTestProvider{}
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Test), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, nil, environment.New("test"),
		mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	virtualEnv := map[string]string{"FIRST": "first", "SECOND": "second", "UNCHANGED": "other"}
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider:   provisioning.Test,
		VirtualEnv: virtualEnv,
		ParamAliases: map[string]string{
			"FIRST": "SECOND", "SECOND": "FIRST", "LOCAL": "FIRST", "UNCHANGED": "MISSING",
		},
	}))

	require.Equal(t, map[string]string{"FIRST": "second", "SECOND": "first", "LOCAL": "first"},
		provider.options.VirtualEnv)
	require.Equal(t, map[string]string{"FIRST": "first", "SECOND": "second", "UNCHANGED": "other"}, virtualEnv)
}

func TestManagerDeployValidatesOutputAliasDestinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		aliases map[string]string
		want    map[string]string
		wantErr string
	}{
		{
			name: "collision with unaliased output", aliases: map[string]string{"FIRST": "SECOND"},
			wantErr: `provider outputs "FIRST" and "SECOND" both target project variable "SECOND"`,
		},
		{
			name: "duplicate explicit destinations", aliases: map[string]string{"FIRST": "SHARED", "SECOND": "SHARED"},
			wantErr: `provider outputs "FIRST" and "SECOND" both target project variable "SHARED"`,
		},
		{
			name: "overlapping aliases applied once", aliases: map[string]string{"FIRST": "SECOND", "SECOND": "SHARED"},
			want: map[string]string{"FIRST": "old-first", "SECOND": "one", "SHARED": "two"},
		},
		{
			name: "identity alias", aliases: map[string]string{"FIRST": "FIRST"},
			want: map[string]string{"FIRST": "one", "SECOND": "two", "SHARED": "old-shared"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			original := map[string]string{"FIRST": "old-first", "SECOND": "old-second", "SHARED": "old-shared"}
			backing := environment.NewWithValues("test", original)
			before := backing.Dotenv()
			provider := &aliasTestProvider{deployment: &provisioning.Deployment{
				Outputs: map[string]provisioning.OutputParameter{
					"FIRST":  {Type: provisioning.ParameterTypeString, Value: "one"},
					"SECOND": {Type: provisioning.ParameterTypeString, Value: "two"},
				},
			}}
			mockContext := mocks.NewMockContext(t.Context())
			mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Test), func() provisioning.Provider {
				return provider
			})
			envManager := &mockenv.MockEnvManager{}
			if tt.wantErr == "" {
				envManager.On("Save", mock.Anything, backing).Return(nil).Once()
			}
			manager := provisioning.NewManager(
				mockContext.Container, defaultProvider, envManager, backing,
				mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
			)
			require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
				Provider: provisioning.Test, OutputAliases: tt.aliases,
			}))

			result, err := manager.Deploy(t.Context())
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Nil(t, result)
				require.Equal(t, before, backing.Dotenv(), "no output should be written on collision")
				envManager.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
			} else {
				require.NoError(t, err)
				require.Same(t, provider.deployment, result.Deployment)
				require.Equal(t, tt.want, backing.Dotenv())
			}
			require.Contains(t, provider.deployment.Outputs, "FIRST")
			require.NotContains(t, provider.deployment.Outputs, "SHARED")
			envManager.AssertExpectations(t)
		})
	}
}

type savingEnvironmentProvider struct {
	provisioning.Provider
	env        environment.ScopedEnvironment
	envManager environment.Manager
}

func (p *savingEnvironmentProvider) Initialize(ctx context.Context, _ string, _ provisioning.Options) error {
	if err := p.env.GetConfig().Set("infra.parameters.input", p.env.Getenv("LOCAL_INPUT")); err != nil {
		return err
	}
	p.env.DotenvSet("PROVIDER_WRITE", "saved")
	if err := p.envManager.Save(ctx, p.env.BackingEnv()); err != nil {
		return err
	}

	return p.envManager.SaveWithOptions(ctx, p.env.BackingEnv(), nil)
}

func (p *savingEnvironmentProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	return (&mappedEnvironmentProvider{}).Deploy(ctx)
}

// BACKCOMPAT: upstream/main lets providers (Bicep in particular) use the live shared environment, its config and
// its env manager directly, while manager outputs are staged in the layer's own environment. If this test breaks,
// providers have lost that direct access: confirm the change is intentional before editing the test.
func TestCompat_ProviderKeepsDirectEnvironmentAccess(t *testing.T) {
	t.Parallel()

	for _, aliases := range []bool{false, true} {
		t.Run(fmt.Sprintf("aliases=%t", aliases), func(t *testing.T) {
			t.Parallel()

			mockContext := mocks.NewMockContext(t.Context())
			sharedEnv := environment.NewWithValues("shared", map[string]string{
				"LOCAL_INPUT": "live-input", "SHARED_INPUT": "live-input",
			})
			mockContext.Container.MustRegisterSingleton(func() environment.ScopedEnvironment {
				return sharedEnv
			})
			parentManager := &mockenv.MockEnvManager{}
			parentManager.On("Save", mock.Anything, sharedEnv).Return(nil).Once()
			parentManager.On("SaveWithOptions", mock.Anything, sharedEnv, (*environment.SaveOptions)(nil)).
				Return(nil).Once()
			mockContext.Container.MustRegisterSingleton(func() environment.Manager {
				return parentManager
			})

			layerEnv := environment.NewWithValues("shared", map[string]string{
				"LOCAL_INPUT": "snapshot-input", "SHARED_INPUT": "snapshot-input",
			})
			layerManager := &mockenv.MockEnvManager{}
			layerManager.On("Save", mock.Anything, layerEnv).Return(nil).Once()
			var provider *savingEnvironmentProvider
			mockContext.Container.MustRegisterNamedTransient(string(provisioning.Test),
				func(env environment.ScopedEnvironment, envManager environment.Manager) provisioning.Provider {
					provider = &savingEnvironmentProvider{env: env, envManager: envManager}
					return provider
				})

			options := provisioning.Options{Provider: provisioning.Test}
			if aliases {
				options.ParamAliases = map[string]string{"LOCAL_INPUT": "SHARED_INPUT"}
				options.OutputAliases = map[string]string{"LOCAL_OUTPUT": "SHARED_OUTPUT"}
			}
			manager := provisioning.NewManager(
				mockContext.Container, defaultProvider, layerManager, layerEnv,
				mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
			)

			require.NoError(t, manager.Initialize(t.Context(), "", options))
			require.Same(t, sharedEnv, provider.env.BackingEnv())
			require.Same(t, parentManager, provider.envManager)
			value, has := sharedEnv.Config.GetString("infra.parameters.input")
			require.True(t, has)
			require.Equal(t, "live-input", value)
			require.Equal(t, "saved", sharedEnv.Getenv("PROVIDER_WRITE"))
			require.Empty(t, layerEnv.Getenv("PROVIDER_WRITE"))
			require.True(t, layerEnv.Config.IsEmpty())

			_, err := manager.Deploy(t.Context())
			require.NoError(t, err)
			outputKey := "LOCAL_OUTPUT"
			if aliases {
				outputKey = "SHARED_OUTPUT"
				require.Empty(t, layerEnv.Getenv("LOCAL_OUTPUT"))
			}
			require.Equal(t, "output-value", layerEnv.Getenv(outputKey))
			require.Empty(t, sharedEnv.Getenv(outputKey), "manager outputs must remain staged in the clone")
			layerManager.AssertExpectations(t)
			parentManager.AssertExpectations(t)
			var resolvedManager environment.Manager
			require.NoError(t, mockContext.Container.Resolve(&resolvedManager))
			require.Same(t, parentManager, resolvedManager)
		})
	}
}

func (p *mappedEnvironmentProvider) Initialize(context.Context, string, provisioning.Options) error {
	p.initializedInput = p.env.Getenv("LOCAL_INPUT")
	return nil
}

func (p *mappedEnvironmentProvider) Deploy(context.Context) (*provisioning.DeployResult, error) {
	return &provisioning.DeployResult{
		Deployment: &provisioning.Deployment{
			Outputs: map[string]provisioning.OutputParameter{
				"LOCAL_OUTPUT": {
					Type:  provisioning.ParameterTypeString,
					Value: "output-value",
				},
			},
		},
	}, nil
}

func (p *mappedEnvironmentProvider) Destroy(
	context.Context,
	provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	return &provisioning.DestroyResult{
		InvalidatedEnvKeys: []string{"LOCAL_OUTPUT"},
	}, nil
}

// BACKCOMPAT: providers keep reading and writing names in the provider view; aliases are applied underneath them by the
// manager. A provider must never need to know about aliases.
func TestCompat_ProviderViewMapsToProjectView(t *testing.T) {
	backing := environment.NewWithValues("test-env", map[string]string{
		"SHARED_INPUT": "input-value",
		"LOCAL_OUTPUT": "unrelated-value",
	})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, backing).Return(nil).Twice()

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterSingleton(func() environment.ScopedEnvironment {
		return backing
	})
	var provider *mappedEnvironmentProvider
	mockContext.Container.MustRegisterNamedTransient(string(provisioning.Test),
		func(env environment.ScopedEnvironment) provisioning.Provider {
			provider = &mappedEnvironmentProvider{env: env}
			return provider
		})

	manager := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		backing,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	options := provisioning.Options{
		Provider:      provisioning.Test,
		ParamAliases:  map[string]string{"LOCAL_INPUT": "SHARED_INPUT"},
		OutputAliases: map[string]string{"LOCAL_OUTPUT": "SHARED_OUTPUT"},
	}

	require.NoError(t, manager.Initialize(t.Context(), "", options))
	require.Equal(t, "input-value", provider.initializedInput,
		"the provider should read LOCAL_INPUT from SHARED_INPUT through its scoped environment")

	_, err := manager.Deploy(t.Context())
	require.NoError(t, err)
	require.Equal(t, "output-value", backing.Getenv("SHARED_OUTPUT"),
		"the provider's LOCAL_OUTPUT should be persisted as SHARED_OUTPUT")
	require.Equal(t, "unrelated-value", backing.Getenv("LOCAL_OUTPUT"),
		"writing the aliased output should not overwrite the backing LOCAL_OUTPUT")

	_, err = manager.Destroy(t.Context(), provisioning.NewDestroyOptions(true, false))
	require.NoError(t, err)
	require.Empty(t, backing.Getenv("SHARED_OUTPUT"),
		"invalidating the provider's LOCAL_OUTPUT should delete SHARED_OUTPUT")
	require.Equal(t, "unrelated-value", backing.Getenv("LOCAL_OUTPUT"),
		"destroying the aliased output should not delete the backing LOCAL_OUTPUT")
	envManager.AssertExpectations(t)
}

func (p *destroyResultProvider) Initialize(context.Context, string, provisioning.Options) error {
	return nil
}

func (p *destroyResultProvider) Destroy(
	context.Context,
	provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	return p.result, nil
}

func TestManagerDestroy_InvalidatesMappedOutput(t *testing.T) {
	t.Parallel()

	const (
		logicalKey  = "OUTPUT"
		physicalKey = "LAYER_OUTPUT"
	)

	backing := environment.NewWithValues("test-env", map[string]string{
		logicalKey:  "shared-value",
		physicalKey: "layer-value",
	})
	scoped := &mappedOutputEnvironment{
		ScopedEnvironment: backing,
		logicalKey:        logicalKey,
		physicalKey:       physicalKey,
	}
	provider := &destroyResultProvider{
		result: &provisioning.DestroyResult{InvalidatedEnvKeys: []string{logicalKey}},
	}

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Test), func() provisioning.Provider {
		return provider
	})

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, backing).Return(nil)

	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		scoped,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	require.NoError(t, mgr.Initialize(t.Context(), "", provisioning.Options{Provider: provisioning.Test}))

	_, err := mgr.Destroy(t.Context(), provisioning.NewDestroyOptions(true, false))

	require.NoError(t, err)
	require.Equal(t, "shared-value", backing.Getenv(logicalKey))
	require.Empty(t, backing.Getenv(physicalKey))
	envManager.AssertExpectations(t)
}

type setupScopedEnvironment struct {
	*environment.Environment
}

func (e *setupScopedEnvironment) GetSubscriptionId() string {
	return "layer-subscription"
}

func (e *setupScopedEnvironment) GetLocation() string {
	return "layer-location"
}

func TestEnsureSubscriptionAndLocation_UsesScopedValuesAndSavesBackingEnvironment(t *testing.T) {
	t.Parallel()

	backing := environment.NewWithValues("shared", map[string]string{
		environment.SubscriptionIdEnvVarName: "backing-subscription",
		environment.LocationEnvVarName:       "backing-location",
	})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, backing).Return(nil).Twice()

	require.NoError(t, provisioning.EnsureSubscriptionAndLocation(
		t.Context(), envManager, &setupScopedEnvironment{Environment: backing},
		noPromptPrompter{}, provisioning.EnsureSubscriptionAndLocationOptions{},
	))
	require.Equal(t, "layer-subscription", backing.GetSubscriptionId())
	require.Equal(t, "layer-location", backing.GetLocation())
	envManager.AssertExpectations(t)
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

func registerContainerDependencies(
	mockContext *mocks.MockContext, env *environment.Environment,
) *mockenv.MockEnvManager {
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
	mockContext.Container.MustRegisterSingleton(func() environment.ScopedEnvironment {
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

	return envManager
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
			name: "different providers across layers record each provider sorted",
			// Input is intentionally in reverse-sorted order (terraform before bicep) so the case
			// verifies the sorting contract, not just de-duplication.
			layers: []provisioning.Options{
				{Provider: provisioning.Terraform},
				{Provider: provisioning.Bicep},
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
			name:            "single explicit pulumi",
			layers:          []provisioning.Options{{Provider: provisioning.Pulumi}},
			defaultProvider: defaultProvider,
			expected:        []string{"pulumi"},
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
