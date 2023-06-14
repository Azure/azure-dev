// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

// Manages the orchestration of infrastructure provisioning
type Manager struct {
	serviceLocator      ioc.ServiceLocator
	env                 *environment.Environment
	console             input.Bioc
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

// Creates a new instance of the Provisioning Manager
func NewManager(
	serviceLocator ioc.ServiceLocator,
	env *environment.Environment,
	console input.Bioc,
	alphaFeatureManager *alpha.FeatureManager,
) *Manager {
	return &Manager{
		serviceLocator:      serviceLocator,
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

	var provider Provider
	err = m.serviceLocator.ResolveNamed(string(m.options.Provider), &provider)
	if err != nil {
		return nil, fmt.Errorf("failed resolving IaC provider '%s': %w", m.options.Provider, err)
	}

	return provider, nil
}
