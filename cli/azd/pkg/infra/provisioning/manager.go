// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
)

// Manages the orchestration of infrastructure provisioning
type Manager struct {
	serviceLocator      ioc.ServiceLocator
	env                 *environment.Environment
	console             input.Console
	prompter            prompt.Prompter
	provider            Provider
	alphaFeatureManager *alpha.FeatureManager
	projectPath         string
	options             *Options
}

func (m *Manager) Initialize(ctx context.Context, projectPath string, options Options) error {
	m.projectPath = projectPath
	m.options = &options

	provider, err := m.newProvider(ctx)
	if err != nil {
		return fmt.Errorf("initializing infrastructure provider: %w", err)
	}

	m.provider = provider
	return m.provider.Initialize(ctx, projectPath, options)
}

// Prepares for an infrastructure provision operation
func (m *Manager) Plan(ctx context.Context) (*DeploymentPlan, error) {
	deploymentPlan, err := m.provider.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("planning infrastructure provisioning: %w", err)
	}

	return deploymentPlan, nil
}

// Gets the latest deployment details for the specified scope
func (m *Manager) State(ctx context.Context) (*StateResult, error) {
	result, err := m.provider.State(ctx)
	if err != nil {
		return nil, fmt.Errorf("error retrieving state: %w", err)
	}

	return result, nil
}

// Deploys the Azure infrastructure for the specified project
func (m *Manager) Deploy(ctx context.Context, plan *DeploymentPlan) (*DeployResult, error) {
	// Apply the infrastructure deployment
	deployResult, err := m.provider.Deploy(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("error deploying infrastructure: %w", err)
	}

	if err := UpdateEnvironment(m.env, deployResult.Deployment.Outputs); err != nil {
		return nil, fmt.Errorf("updating environment with deployment outputs: %w", err)
	}

	// make sure any spinner is stopped
	m.console.StopSpinner(ctx, "", input.StepDone)

	return deployResult, nil
}

// Deploys the Azure infrastructure for the specified project
func (m *Manager) WhatIfDeploy(ctx context.Context, plan *DeploymentPlan) (*DeployPreviewResult, error) {
	// Apply the infrastructure deployment
	deployResult, err := m.provider.WhatIfDeploy(ctx, plan)

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
	if err := m.env.Save(); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return destroyResult, nil
}

// EnsureSubscriptionAndLocation ensures that that that subscription (AZURE_SUBSCRIPTION_ID) and location (AZURE_LOCATION)
// variables are set in the environment, prompting the user for the values if they do not exist.
func EnsureSubscriptionAndLocation(ctx context.Context, env *environment.Environment, prompter prompt.Prompter) error {
	if env.GetSubscriptionId() == "" {
		subscriptionId, err := prompter.PromptSubscription(ctx, "Select an Azure Subscription to use:")
		if err != nil {
			return err
		}

		env.SetSubscriptionId(subscriptionId)

		if err := env.Save(); err != nil {
			return err
		}
	}

	if env.GetLocation() == "" {
		location, err := prompter.PromptLocation(
			ctx,
			env.GetSubscriptionId(),
			"Select an Azure location to use:",
			func(_ account.Location) bool { return true },
		)
		if err != nil {
			return err
		}

		env.SetLocation(location)

		if err := env.Save(); err != nil {
			return err
		}
	}

	return nil
}

// Creates a new instance of the Provisioning Manager
func NewManager(
	serviceLocator ioc.ServiceLocator,
	env *environment.Environment,
	console input.Console,
	alphaFeatureManager *alpha.FeatureManager,
	prompter prompt.Prompter,
) *Manager {
	return &Manager{
		serviceLocator:      serviceLocator,
		env:                 env,
		console:             console,
		alphaFeatureManager: alphaFeatureManager,
		prompter:            prompter,
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

	var provider Provider
	err = m.serviceLocator.ResolveNamed(string(m.options.Provider), &provider)
	if err != nil {
		return nil, fmt.Errorf("failed resolving IaC provider '%s': %w", m.options.Provider, err)
	}

	return provider, nil
}
