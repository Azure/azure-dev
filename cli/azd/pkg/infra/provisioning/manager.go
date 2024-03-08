// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
)

type DefaultProviderResolver func() (ProviderKind, error)

// Manages the orchestration of infrastructure provisioning
type Manager struct {
	serviceLocator      ioc.ServiceLocator
	defaultProvider     DefaultProviderResolver
	envManager          environment.Manager
	env                 *environment.Environment
	console             input.Console
	provider            Provider
	alphaFeatureManager *alpha.FeatureManager
	projectPath         string
	options             *Options
}

// defaultOptions for this package.
const (
	defaultModule = "main"
	defaultPath   = "infra"
)

func (m *Manager) Initialize(ctx context.Context, projectPath string, options Options) error {
	// applied defaults if missing
	if options.Module == "" {
		options.Module = defaultModule
	}
	if options.Path == "" {
		options.Path = defaultPath
	}

	m.projectPath = projectPath
	m.options = &options

	provider, err := m.newProvider(ctx)
	if err != nil {
		return fmt.Errorf("initializing infrastructure provider: %w", err)
	}

	m.provider = provider
	return m.provider.Initialize(ctx, projectPath, options)
}

// Gets the latest deployment details for the specified scope
func (m *Manager) State(ctx context.Context, options *StateOptions) (*StateResult, error) {
	result, err := m.provider.State(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("error retrieving state: %w", err)
	}

	return result, nil
}

// Deploys the Azure infrastructure for the specified project
func (m *Manager) Deploy(ctx context.Context) (*DeployResult, error) {
	// Apply the infrastructure deployment
	deployResult, err := m.provider.Deploy(ctx)
	if err != nil {
		return nil, fmt.Errorf("error deploying infrastructure: %w", err)
	}

	skippedDueToDeploymentState := deployResult.SkippedReason == DeploymentStateSkipped

	if skippedDueToDeploymentState {
		m.console.StopSpinner(ctx, "Didn't find new changes.", input.StepSkipped)
	}

	if err := m.UpdateEnvironment(ctx, deployResult.Deployment.Outputs); err != nil {
		return nil, fmt.Errorf("updating environment with deployment outputs: %w", err)
	}

	// make sure any spinner is stopped
	m.console.StopSpinner(ctx, "", input.StepDone)

	return deployResult, nil
}

// Preview generates the list of changes to be applied as part of the provisioning.
func (m *Manager) Preview(ctx context.Context) (*DeployPreviewResult, error) {
	// Apply the infrastructure deployment
	deployResult, err := m.provider.Preview(ctx)

	if err != nil {
		return nil, fmt.Errorf("error deploying infrastructure: %w", err)
	}

	// apply resource mapping
	filteredResult := DeployPreviewResult{
		Preview: &DeploymentPreview{
			Status:     deployResult.Preview.Status,
			Properties: &DeploymentPreviewProperties{},
		},
	}

	for index, result := range deployResult.Preview.Properties.Changes {
		mappingName := infra.GetResourceTypeDisplayName(infra.AzureResourceType(result.ResourceType))
		if mappingName == "" {
			// ignore
			continue
		}
		deployResult.Preview.Properties.Changes[index].ResourceType = mappingName
		filteredResult.Preview.Properties.Changes = append(
			filteredResult.Preview.Properties.Changes, deployResult.Preview.Properties.Changes[index])
	}

	// make sure any spinner is stopped
	m.console.StopSpinner(ctx, "", input.StepDone)

	return &filteredResult, nil
}

// Destroys the Azure infrastructure for the specified project
func (m *Manager) Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error) {
	destroyResult, err := m.provider.Destroy(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("error deleting Azure resources: %w", err)
	}

	// Remove any outputs from the template from the environment since destroying the infrastructure
	// invalidated them all.
	for _, key := range destroyResult.InvalidatedEnvKeys {
		m.env.DotenvDelete(key)
	}

	// Update environment files to remove invalid infrastructure parameters
	if err := m.envManager.Save(ctx, m.env); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return destroyResult, nil
}

func (m *Manager) UpdateEnvironment(
	ctx context.Context,
	outputs map[string]OutputParameter,
) error {
	if len(outputs) > 0 {
		for key, param := range outputs {
			// Complex types marshalled as JSON strings, simple types marshalled as simple strings
			if param.Type == ParameterTypeArray || param.Type == ParameterTypeObject {
				bytes, err := json.Marshal(param.Value)
				if err != nil {
					return fmt.Errorf("invalid value for output parameter '%s' (%s): %w", key, string(param.Type), err)
				}
				m.env.DotenvSet(key, string(bytes))
			} else {
				m.env.DotenvSet(key, fmt.Sprintf("%v", param.Value))
			}
		}

		if err := m.envManager.Save(ctx, m.env); err != nil {
			return fmt.Errorf("writing environment: %w", err)
		}
	}

	return nil
}

// EnsureSubscriptionAndLocation ensures that that that subscription (AZURE_SUBSCRIPTION_ID) and location (AZURE_LOCATION)
// variables are set in the environment, prompting the user for the values if they do not exist.
func EnsureSubscriptionAndLocation(
	ctx context.Context,
	envManager environment.Manager,
	env *environment.Environment,
	prompter prompt.Prompter,
	locationFiler prompt.LocationFilterPredicate,
) error {
	subId := env.GetSubscriptionId()
	if subId == "" {
		subscriptionId, err := prompter.PromptSubscription(ctx, "Select an Azure Subscription to use:")
		if err != nil {
			return err
		}
		subId = subscriptionId
	}
	// GetSubscriptionId() can get the value from the .env file or from system environment.
	// We want to ensure that, if the value came from the system environment, it is persisted in the .env file.
	// By doing this, we ensure that any command depending on .env values does not need to read system env.
	// For example, on CI, when running `azd provision`, we want the .env to have the subscription id and location
	// so that `azd deploy` can just use the values from .env w/o checking os-env again.
	env.SetSubscriptionId(subId)
	if err := envManager.Save(ctx, env); err != nil {
		return err
	}

	location := env.GetLocation()
	if env.GetLocation() == "" {
		loc, err := prompter.PromptLocation(
			ctx,
			env.GetSubscriptionId(),
			"Select an Azure location to use:",
			locationFiler,
		)
		if err != nil {
			return err
		}
		location = loc
	}

	// Same as before, this make sure the location is persisted in the .env file.
	env.SetLocation(location)
	return envManager.Save(ctx, env)
}

// Creates a new instance of the Provisioning Manager
func NewManager(
	serviceLocator ioc.ServiceLocator,
	defaultProvider DefaultProviderResolver,
	envManager environment.Manager,
	env *environment.Environment,
	console input.Console,
	alphaFeatureManager *alpha.FeatureManager,
) *Manager {
	return &Manager{
		serviceLocator:      serviceLocator,
		defaultProvider:     defaultProvider,
		envManager:          envManager,
		env:                 env,
		console:             console,
		alphaFeatureManager: alphaFeatureManager,
	}
}

func (m *Manager) newProvider(ctx context.Context) (Provider, error) {
	var err error
	m.options.Provider, err = ParseProvider(m.options.Provider)
	if err != nil {
		return nil, err
	}

	if alphaFeatureId, isAlphaFeature := alpha.IsFeatureKey(string(m.options.Provider)); isAlphaFeature {
		if !m.alphaFeatureManager.IsEnabled(alphaFeatureId) {
			return nil, fmt.Errorf("provider '%s' is alpha feature and it is not enabled. Run `%s` to enable it.",
				m.options.Provider,
				alpha.GetEnableCommand(alphaFeatureId),
			)
		}

		m.console.WarnForFeature(ctx, alphaFeatureId)
	}

	providerKey := m.options.Provider
	if providerKey == NotSpecified {
		defaultProvider, err := m.defaultProvider()
		if err != nil {
			return nil, err
		}

		providerKey = defaultProvider
	}

	var provider Provider
	err = m.serviceLocator.ResolveNamed(string(providerKey), &provider)
	if err != nil {
		return nil, fmt.Errorf("failed resolving IaC provider '%s': %w", providerKey, err)
	}

	return provider, nil
}
